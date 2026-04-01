package cluster

import (
	"context"
	"fmt"

	ptpv1 "github.com/k8snetworkplumbingwg/ptp-operator/api/v1"
	ptpclient "github.com/k8snetworkplumbingwg/ptp-operator/pkg/client/clientset/versioned/typed/ptp/v1"
	"github.com/redhat-cne/ptpgen/pkg/config"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
)

// testPtpConfigNames are the PtpConfig names that ptpgen manages.
var testPtpConfigNames = []string{
	config.PtpGrandMasterPolicyName,
	config.PtpWPCGrandMasterPolicyName,
	config.PtpBcMaster1PolicyName,
	config.PtpBcMaster2PolicyName,
	config.PtpSlave1PolicyName,
	config.PtpSlave2PolicyName,
	config.PtpDualNicBCHAPolicyName,
}

// allNodeLabels are the node labels that ptpgen manages.
var allNodeLabels = []string{
	config.PtpGrandmasterNodeLabel,
	config.PtpClockUnderTestNodeLabel,
	config.PtpSlave1NodeLabel,
	config.PtpSlave2NodeLabel,
}

// Clean removes all test PtpConfigs and node labels created by ptpgen.
func Clean(coreClient corev1client.CoreV1Interface, ptpClient ptpclient.PtpV1Interface, namespace string) error {
	logrus.Info("Cleaning existing test PtpConfigs and node labels...")

	// Delete PtpConfigs
	for _, name := range testPtpConfigNames {
		err := ptpClient.PtpConfigs(namespace).Delete(context.Background(), name, metav1.DeleteOptions{})
		if err != nil {
			logrus.Debugf("PtpConfig %s not found or already deleted: %v", name, err)
		} else {
			logrus.Infof("Deleted PtpConfig %s", name)
		}
	}

	// Remove node labels
	for _, label := range allNodeLabels {
		if err := deleteLabel(coreClient, label); err != nil {
			return fmt.Errorf("failed to delete label %s: %w", label, err)
		}
	}

	logrus.Info("Clean complete")
	return nil
}

// LabelNodes applies node labels based on the generated configs.
// It inspects each PtpConfig's Recommend.Match rules to find which label
// is needed, then labels the corresponding node.
func LabelNodes(coreClient corev1client.CoreV1Interface, configs []ptpv1.PtpConfig, nodeLabels map[string]string) error {
	for nodeName, label := range nodeLabels {
		logrus.Infof("Labeling node %s with %s", nodeName, label)
		if err := labelNode(coreClient, nodeName, label); err != nil {
			return fmt.Errorf("failed to label node %s with %s: %w", nodeName, label, err)
		}
	}
	return nil
}

// Apply creates the PtpConfig resources on the cluster.
func Apply(ptpClient ptpclient.PtpV1Interface, namespace string, configs []ptpv1.PtpConfig) error {
	for i := range configs {
		cfg := &configs[i]
		cfg.Namespace = namespace
		name := cfg.Name
		logrus.Infof("Creating PtpConfig %s in namespace %s", name, namespace)
		_, err := ptpClient.PtpConfigs(namespace).Create(context.Background(), cfg, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create PtpConfig %s: %w", name, err)
		}
	}
	logrus.Info("All PtpConfigs applied successfully")
	return nil
}

func deleteLabel(coreClient corev1client.CoreV1Interface, label string) error {
	nodeList, err := coreClient.Nodes().List(context.Background(), metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=", label),
	})
	if err != nil {
		return fmt.Errorf("failed to list nodes with label %s: %w", label, err)
	}
	for i := range nodeList.Items {
		node := &nodeList.Items[i]
		delete(node.Labels, label)
		_, err = coreClient.Nodes().Update(context.Background(), node, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update node %s: %w", node.Name, err)
		}
		logrus.Infof("Removed label %s from node %s", label, node.Name)
	}
	return nil
}

func labelNode(coreClient corev1client.CoreV1Interface, nodeName, label string) error {
	node, err := coreClient.Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get node %s: %w", nodeName, err)
	}
	if node.Labels == nil {
		node.Labels = make(map[string]string)
	}
	node.Labels[label] = ""
	_, err = coreClient.Nodes().Update(context.Background(), node, metav1.UpdateOptions{})
	return err
}
