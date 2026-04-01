package client

import (
	"fmt"
	"os"

	ptpclient "github.com/k8snetworkplumbingwg/ptp-operator/pkg/client/clientset/versioned/typed/ptp/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// ClientSet holds the Kubernetes clients needed for ptpgen.
type ClientSet struct {
	corev1client.CoreV1Interface
	ptpclient.PtpV1Interface
	Config *rest.Config
}

// New creates a ClientSet from the given kubeconfig path.
// If kubeconfig is empty, it falls back to the KUBECONFIG env var,
// then to in-cluster config.
func New(kubeconfig string) (*ClientSet, error) {
	var config *rest.Config
	var err error

	if kubeconfig == "" {
		kubeconfig = os.Getenv("KUBECONFIG")
	}

	if kubeconfig != "" {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		config, err = rest.InClusterConfig()
	}
	if err != nil {
		return nil, fmt.Errorf("failed to build kubeconfig: %w", err)
	}

	cs := &ClientSet{Config: config}
	cs.CoreV1Interface = corev1client.NewForConfigOrDie(config)
	cs.PtpV1Interface = ptpclient.NewForConfigOrDie(config)
	return cs, nil
}
