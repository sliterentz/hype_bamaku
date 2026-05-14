package k8s

import (
	"os/exec"
	"strings"
	"testing"
)

func TestConfigMapUpdateLogic(t *testing.T) {
	// Simulate the original ConfigMap yaml
	originalYaml := `apiVersion: v1
data:
  ClusterConfiguration: |
    apiServer:
      certSANs:
      - 10.0.0.10
      extraArgs:
        authorization-mode: Node,RBAC
      timeoutForControlPlane: 4m0s
    controlPlaneEndpoint: 10.0.0.10:6443
    kubernetesVersion: v1.31.0
    networking:
      dnsDomain: cluster.local
      podSubnet: 192.168.0.0/16
kind: ConfigMap
metadata:
  name: kubeadm-config
  namespace: kube-system
`

	// Create a temporary file with the original yaml
	err := exec.Command("bash", "-c", "echo '"+originalYaml+"' > /tmp/kubeadm-config-test.yaml").Run()
	if err != nil {
		t.Skip("Skipping test: bash not available or failed to create temp file")
	}

	// Run the sed command
	expectedDomain := "rifcloud.fantastickim.dev"
	sedCmd := "sed -i 's|controlPlaneEndpoint:.*|controlPlaneEndpoint: " + expectedDomain + ":6443|g' /tmp/kubeadm-config-test.yaml"
	err = exec.Command("bash", "-c", sedCmd).Run()
	if err != nil {
		t.Fatalf("Failed to run sed command: %v", err)
	}

	// Read the modified file
	out, err := exec.Command("bash", "-c", "cat /tmp/kubeadm-config-test.yaml").Output()
	if err != nil {
		t.Fatalf("Failed to read modified file: %v", err)
	}

	// Verify the endpoint was updated
	modifiedYaml := string(out)
	if !strings.Contains(modifiedYaml, "controlPlaneEndpoint: "+expectedDomain+":6443") {
		t.Errorf("ConfigMap was not updated correctly. Got:\n%s", modifiedYaml)
	}
	if strings.Contains(modifiedYaml, "controlPlaneEndpoint: 10.0.0.10:6443") {
		t.Errorf("Original controlPlaneEndpoint was not replaced")
	}
}
