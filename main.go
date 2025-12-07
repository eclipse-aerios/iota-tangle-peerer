package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/caarlos0/env/v9"
	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type envConfig struct {
	MyNodeName string `env:"MY_NODE_NAME,notEmpty"`
	MyIP       string `env:"MY_IP,notEmpty"`
}

func main() {
	fmt.Println("Starting")
	var mainNodeName string
	var iotaHornetSelector string
	var iotaHornetNs string
	var privateKeyFile string
	var refreshPeriod time.Duration
	var retryPeriod time.Duration
	var hornetRestApiPort int
	var gossipProtocolPort int

	flag.StringVar(&mainNodeName, "main-node-name", "", "Name of k8s node hosting main hornet pod")
	flag.StringVar(&iotaHornetSelector, "iota-hornet-selector", "", "label selector for iota-hornet daemonset")
	flag.StringVar(&iotaHornetNs, "iota-hornet-ns", "", "namespace with iota-hornet daemonset")
	flag.StringVar(&privateKeyFile, "private-key-file", "", "path to private key file of this hornet node")
	flag.DurationVar(&refreshPeriod, "refresh-period", 5*time.Minute, "Period between checks if peering needs to be reestabilished. In go duration format. Default: 5m")
	flag.DurationVar(&retryPeriod, "retry-period", 5*time.Second, "Period between retries of peering estabilishment. In go duration format. Default: 5s")
	flag.IntVar(&hornetRestApiPort, "hornet-rest-api-port", 14265, "Port on which main node's hornet rest API is exposed. Default: 14265")
	flag.IntVar(&gossipProtocolPort, "gossip-protocol-port", 15600, "Port of hornet gossip protocol. Included in multiaddress. Default: 15600")
	flag.Parse()

	envCfg := envConfig{}
	if err := env.Parse(&envCfg); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to parse env variables: %s\n", err.Error())
	}
	fmt.Println("Loaded env and flags")
	if mainNodeName == "" {
		fmt.Fprintf(os.Stderr, "mainNodeName not specified\n")
		os.Exit(1)
	}
	if iotaHornetSelector == "" {
		fmt.Fprintf(os.Stderr, "iotaHornetSelector not specified\n")
		os.Exit(1)
	}
	if iotaHornetNs == "" {
		fmt.Fprintf(os.Stderr, "iotaHornetNs not specified\n")
		os.Exit(1)
	}
	if privateKeyFile == "" {
		fmt.Fprintf(os.Stderr, "privateKeyFile not specified\n")
		os.Exit(1)
	}
	fmt.Println("validated flags")

	if mainNodeName == envCfg.MyNodeName {
		fmt.Fprintf(os.Stderr, "Is main node, not running\n")
		for {
			time.Sleep(10000 * time.Hour)
		}
	}

	config, err := rest.InClusterConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load k8s inClusterConfig, is container ran outside cluster? err: %s\n", err.Error())
		os.Exit(1)
	}
	k8sClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create clientSet for k8s cluster config, err: %s\n", err.Error())
		os.Exit(1)
	}
	multiaddress, err := calculateMultiaddress(privateKeyFile, envCfg.MyIP, gossipProtocolPort)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to construct multiaddress, err: %s\n", err.Error())
		os.Exit(1)
	}
	fmt.Printf("Multiaddress is %s\n", multiaddress)

	for ; ; time.Sleep(refreshPeriod) {
		for done := false; !done; time.Sleep(retryPeriod) {
			done = tryPeering(k8sClient, envCfg.MyNodeName, mainNodeName, iotaHornetSelector, iotaHornetNs, multiaddress, hornetRestApiPort)
		}
	}
}

func calculateMultiaddress(privateKeyFile, myIp string, gossipProtocolPort int) (string, error) {
	for {
		if _, err := os.Stat(privateKeyFile); !os.IsNotExist(err) {
			break
		}
		fmt.Printf("Waiting for file %s to be created\n", privateKeyFile)
		time.Sleep(5 * time.Second)
	}
	// private key already exists, load and return it
	privKey, err := readEd25519PrivateKeyFromPEMFile(privateKeyFile)
	if err != nil {
		return "", errors.Wrapf(err, "unable to load Ed25519 private key for peer identity")
	}
	libp2pPrivKey, _, err := libp2pcrypto.KeyPairFromStdKey(&privKey)
	if err != nil {
		return "", errors.Wrapf(err, "Failed to get libp2pkey from ed25519 key")
	}
	peerID, err := libp2ppeer.IDFromPrivateKey(libp2pPrivKey)
	if err != nil {
		return "", errors.Wrapf(err, "Failed to get Peer ID from private key")
	}
	multiaddress := fmt.Sprintf("/ip4/%s/tcp/%d/p2p/%s", myIp, gossipProtocolPort, peerID)
	return multiaddress, nil
}

// ReadEd25519PrivateKeyFromPEMFile reads an Ed25519 private key from a file with PEM format.
func readEd25519PrivateKeyFromPEMFile(filepath string) (ed25519.PrivateKey, error) {

	pemPrivateBlockBytes, err := os.ReadFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("unable to read private key: %w", err)
	}

	pemPrivateBlock, _ := pem.Decode(pemPrivateBlockBytes)
	if pemPrivateBlock == nil {
		return nil, errors.New("unable to decode private key")
	}

	cryptoPrivKey, err := x509.ParsePKCS8PrivateKey(pemPrivateBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("unable to parse private key: %w", err)
	}

	privKey, ok := cryptoPrivKey.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("unable to type assert private key")
	}

	return privKey, nil
}

func tryPeering(k8sClient *kubernetes.Clientset, myNodeName, mainNodeName, iotaHornetSelector, iotaHornetNs, multiaddress string, hornetRestApiPort int) bool {
	fmt.Println("Getting main hornet node")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	podList, err := k8sClient.CoreV1().Pods(iotaHornetNs).List(ctx, metav1.ListOptions{
		LabelSelector: iotaHornetSelector,
		FieldSelector: "spec.nodeName=" + mainNodeName,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to list pods matching labels %s and nodeName %s, err: %s\n", iotaHornetSelector, mainNodeName, err.Error())
		return false
	}
	if len(podList.Items) != 1 {
		fmt.Fprintf(os.Stderr, "There is not exactly 1 main hornet pod (%d exist), will try again later\n", len(podList.Items))
		return false
	}
	mainHornet := podList.Items[0]
	mainHornetIP := mainHornet.Status.PodIP
	if mainHornetIP == "" {
		fmt.Fprintf(os.Stderr, "Main hornet pod has no IP, will try again later\n")
		return false
	}
	fmt.Println("Checking current peers")
	url := fmt.Sprintf("http://%s:%d/api/core/v2/peers", mainHornetIP, hornetRestApiPort)

	req, err := http.NewRequest("GET", url, nil)

	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to build get to main node, will try again later, err: %s\n", err.Error())
		return false
	}
	req.Header.Add("Accept", "application/json")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get main node peers, will try again later, err: %s\n", err.Error())
		return false
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "Unexpected status when getting main node peers: %d, body: %s, will try again later\n", res.StatusCode, readBody(res.Body))
		return false
	}
	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read get response body, will try again later, err: %s\n", err.Error())
		return false
	}

	var peers []map[string]interface{}
	err = json.Unmarshal(bodyBytes, &peers)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to parse get peers response, will try again later, err: %s\n", err.Error())
		return false
	}
	fmt.Println("Gathered current peers")
	for _, peer := range peers {
		if alias, ok := peer["alias"].(string); ok && alias == myNodeName {
			fmt.Println("Already peered with main node, checking if valid peerID")
			multiaddressInMain, ok := getMultiaddressFromPeer(peer)
			if !ok {
				fmt.Fprintf(os.Stderr, "Multiaddress not found in peer corresponding to this node (%s) in get peers response. Will try again later.\n", alias)
				return false
			}
			peerIdInMain, ok := peer["id"].(string)
			if !ok {
				fmt.Fprintf(os.Stderr, "PeerID not found in peer corresponding to this node (%s) in get peers response. Will try again later.\n", alias)
				return false
			}
			fullMultiInMain := multiaddressInMain + "/p2p/" + peerIdInMain
			if fullMultiInMain == multiaddress {
				return true
			}
			fmt.Printf("Multiaddress in main node is stale (my (%s) != in main (%s)), deleting old peering\n", multiaddress, fullMultiInMain)
			url := fmt.Sprintf("http://%s:%d/api/core/v2/peers/%s", mainHornetIP, hornetRestApiPort, peerIdInMain)
			req, err := http.NewRequest("DELETE", url, nil)

			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to build delete peering request, will try again later, err: %s\n", err.Error())
				return false
			}
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to delete old peering, will try again later, err: %s\n", err.Error())
				return false
			}
			defer res.Body.Close()

			if res.StatusCode != 204 {
				fmt.Fprintf(os.Stderr, "Unexpected status when deleting old peering id: %d, body: %s will try again later\n", res.StatusCode, readBody(res.Body))
				return false
			}
			fmt.Println("Old peering deleted")
		}
	}
	fmt.Println("Establishing peering")
	payload, _ := json.Marshal(map[string]string{
		"multiAddress": multiaddress,
		"alias":        myNodeName,
	})

	req, err = http.NewRequest("POST", url, bytes.NewBuffer(payload))

	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to build peering request, will try again later, err: %s\n", err.Error())
		return false
	}
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("Accept", "application/json")

	res, err = http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to post peering request, will try again later, err: %s\n", err.Error())
		return false
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "Unexpected status when getting main node peers: %d, body: %s, will try again later\n", res.StatusCode, readBody(res.Body))
		return false
	}
	fmt.Println("Peering established")
	return true
}

func readBody(bodyReader io.Reader) string {
	bodyBytes, err := io.ReadAll(bodyReader)
	if err != nil {
		return "Failed to read body: " + err.Error()
	} else {
		return string(bodyBytes)
	}
}

func getMultiaddressFromPeer(peer map[string]interface{}) (string, bool) {
	raw, ok := peer["multiAddress"]
	if !ok {
		return "", false
	}
	list, ok := raw.([]interface{})
	if !ok || len(list) == 0 {
		return "", false
	}
	str, ok := list[0].(string)
	if !ok {
		return "", false
	}
	return str, true
}
