// main.go
// Copyright (C) 2022 rmelo <Ricardo Melo <rmelo@cropa.ca>
//
// Distributed under terms of the MIT license.
//

package main

import (
	//"context"

	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	clientset "k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"k8s.io/klog"
	kubeadmcmd "k8s.io/kubernetes/cmd/kubeadm/app/cmd"
	cmdutil "k8s.io/kubernetes/cmd/kubeadm/app/cmd/util"
	"k8s.io/kubernetes/cmd/kubeadm/app/util/apiclient"
	kubeconfigutil "k8s.io/kubernetes/cmd/kubeadm/app/util/kubeconfig"
)

var (
	useHeaderIP     bool
	ipSourceHeader  string
	certKey         string
	valitationRegex string
)

func getClientset(file string, dryRun bool) (clientset.Interface, error) {
	if dryRun {
		dryRunGetter, err := apiclient.NewClientBackedDryRunGetterFromKubeconfig(file)
		if err != nil {
			return nil, err
		}
		return apiclient.NewDryRunClient(dryRunGetter, os.Stdout), nil
	}
	return kubeconfigutil.ClientSetFromFile(file)
}

// Generate kubeadm command.
func genJoinCmd(w io.Writer, isMaster bool) error {
	var kubeConfigFile string
	var dryRun bool
	var certificateKey string
	var cfgPath string
	cfg := cmdutil.DefaultInitConfiguration()
	log.Println("Calling add_master().")

	klog.V(1).Infoln("[token] getting Clientsets from kubeconfig file")
	kubeConfigFile = cmdutil.GetKubeConfigPath(kubeConfigFile)
	client, err := getClientset(kubeConfigFile, dryRun)
	if err != nil {
		fmt.Println(err)
		return err
	}
	out := new(bytes.Buffer)
	err = kubeadmcmd.RunCreateToken(out, client, cfgPath, cfg, true, certificateKey, kubeConfigFile)
	if err != nil {
		return err
	}
	if isMaster {
		w.Write(bytes.TrimRight(out.Bytes(), "\n"))
		w.Write([]byte(fmt.Sprintf(" --control-plane --certificate-key %s", certKey)))
	} else {
		w.Write(out.Bytes())
	}
	return nil
}

// Validate client.
func validClient(req *http.Request) error {
	var sourceIP string
	if !useHeaderIP {
		ip, _, err := net.SplitHostPort(req.RemoteAddr)
		if err != nil {
			fmt.Printf("userip: %q is not IP:port\n", req.RemoteAddr)
			return err
		}

		userIP := net.ParseIP(ip)
		if userIP == nil {
			fmt.Printf("userip: %q is not IP:port\n", req.RemoteAddr)
			return errors.New(fmt.Sprintf("userip: %q is not IP:port\n", req.RemoteAddr))
		}
		sourceIP = ip
	} else {

		// This will only be defined when site is accessed via non-anonymous proxy
		// and takes precedence over RemoteAddr
		// Header.Get is case-insensitive
		forward := req.Header.Get(ipSourceHeader)

		if forward == "" {
			return errors.New(fmt.Sprintf("Missing header %v\n", ipSourceHeader))
		}
		//fmt.Printf("Forwarded for: %s\n", forward)
		userIP := net.ParseIP(forward)
		if userIP == nil {
			return errors.New(fmt.Sprintf("Invalid IP from http header %v: %v\n", ipSourceHeader, forward))
		}
		sourceIP = forward
	}
	r, _ := regexp.Compile(valitationRegex)
	ptr, _ := net.LookupAddr(sourceIP)
	for _, ptrvalue := range ptr {
		fmt.Println(ptrvalue)
		fmt.Printf("r.MatchString(%v) = %v\n", ptrvalue, r.MatchString(ptrvalue))
		if r.MatchString(ptrvalue) {
			return nil
		}
	}

	return errors.New(fmt.Sprintf("Host FQDN found for %v didn't match validation condition (%v).\n", sourceIP, valitationRegex))
}

// Generate kubeadm command to join k8s cluster.
func join_cmd_request(w http.ResponseWriter, req *http.Request) {
	var isMaster bool
	out := new(bytes.Buffer)
	// Validate is client is allowed to make this call.
	err := validClient(req)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(err.Error()))
		return
	}
	query := req.URL.Query()
	fmt.Printf("Query=%v\n", query)
	tmp, present := query["NEWMASTER"]
	if !present || len(tmp) == 0 {
		isMaster = false
	} else {
		isMaster = strings.ToUpper(tmp[0]) == "TRUE"
	}
	fmt.Printf("isMaster=%v\n", isMaster)
	err = genJoinCmd(out, isMaster)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(err.Error()))
		return
	}
	w.Write(out.Bytes())
}

// LookupEnvOrString returns the value from env variable key is exists or defaultVal as string
func LookupEnvOrString(key string, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return defaultVal
}

// LookupEnvOrBool returns the value from env variable key is exists or defaultVal as string
func LookupEnvOrBool(key string, defaultVal bool) bool {
	if val, ok := os.LookupEnv(key); ok {
		newVal, err := strconv.ParseBool(val)
		if err == nil {
			return newVal
		}
	}
	return defaultVal
}

// Main function.
func main() {

	var listenAddr string
	flag.BoolVar(&useHeaderIP, "useHeader", LookupEnvOrBool("KGEN_USE_HEADER", false), "Use HTTP header request to get client IP. Useful if you are behind a proxy.")
	flag.StringVar(&ipSourceHeader, "header", LookupEnvOrString("KGEN_HEADER", "X-Forwarding-for"), "Used with '--useHeader' to define header field from where you should get the client source IP.")
	flag.StringVar(&valitationRegex, "match", LookupEnvOrString("KGEN_MATCH", "^.*$"), "Regex used to validate if the request should be processed")
	flag.StringVar(&listenAddr, "addr", LookupEnvOrString("KGEN_ADDR", ":8000"), "[ip]:port used to accept http requests.")
	flag.StringVar(&certKey, "cert-key", LookupEnvOrString("KGEN_CERT", ""), "Certificate key used to add new control plane nodes.")

	flag.Parse()

	log.Println("Starting main().")
	http.HandleFunc("/join", join_cmd_request)

	log.Fatal(http.ListenAndServe(listenAddr, nil))
}
