package config

import (
    "fmt"
    "net"
    "os"
    "strconv"

	"github.com/joho/godotenv"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	pulumiConfig "github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type Config struct {
	// Hyper-V Configuration
	HyperVNodeCount    	int
	HyperVCPUs         	int
	HyperVMemoryGB     	int
	HyperVDiskGB       	int
	HyperVNetworkSwitch string
	HyperVImageURL     	string

	// Kubernetes Control Plane Configuration
    K8sCPHostnamePrefix string
	K8sCPIPStart        string
    K8sCPIPEnd          string
    K8sCPIPCIDR         int
	K8sVersion         	string
	K8sPodCIDR         	string
	K8sServiceCIDR     	string
	
	// Virtual IP Configuration
	K8sDOMAIN          	string
    K8sVIP                string
    K8sVIPInterface       string
    K8sVIPARPEnabled      bool
    K8sVIPLeaderElection  bool

	// MetalLB Configuration
    K8sMetalLBEnabled        bool
    K8sMetalLBIPRangeStart   string
    K8sMetalLBIPRangeEnd     string
    K8sMetalLBAddressPoolName string

	// SSH Configuration
    SSHUser           string
	SSHPrivateKey     string

	// GitOps Configuration
	GitOpsRepoURL      	string
	GitOpsBranch       	string
	ArgoCDProjectPrefix string

    // Stack Metadata
    PulumiStack   string
    PulumiBackend string
	
	// Environment Mode
    Environment string // "development", "staging", "production"
    MockMode    bool   // Enable mock IPs for testing
}

func LoadConfig(ctx *pulumi.Context) (*Config, error) {
    // Load .env file dengan error handling yang lebih baik
    envPath := ".env"
    if customPath := os.Getenv("ENV_FILE_PATH"); customPath != "" {
        envPath = customPath
    }

    if err := godotenv.Load(envPath); err != nil {
        // Hanya warning jika file tidak ada, bukan error lain
        if os.IsNotExist(err) {
            fmt.Printf("ℹ️  Info: %s not found, using environment variables or defaults\n", envPath)
        } else {
            // Error lain (permission, format, dll) harus di-handle
            return nil, fmt.Errorf("failed to load %s: %w", envPath, err)
        }
    } else {
        fmt.Printf("✅ Loaded configuration from %s\n", envPath)
    }

    cfg := &Config{}
	pulumiCfg := pulumiConfig.New(ctx, "")

    // Hyper-V Configuration
    cfg.HyperVNodeCount = getEnvAsInt("HYPERV_NODE_COUNT", 2)
    cfg.HyperVCPUs = getEnvAsInt("HYPERV_CPUS", 4)
    cfg.HyperVMemoryGB = getEnvAsInt("HYPERV_MEMORY_GB", 8)
    cfg.HyperVDiskGB = getEnvAsInt("HYPERV_DISK_GB", 50)
    cfg.HyperVNetworkSwitch = getEnv("HYPERV_NETWORK_SWITCH", "DefaultSwitch")
    cfg.HyperVImageURL = getEnv("HYPERV_IMAGE_URL", "")

    // Kubernetes Control Plane Configuration
    cfg.K8sCPHostnamePrefix = getEnv("K8S_CP_HOSTNAME_PREFIX", "k8s-ha-cp")
    cfg.K8sCPIPStart = getEnv("K8S_CP_IP_START", "192.168.1.101")
    cfg.K8sCPIPEnd = getEnv("K8S_CP_IP_END", "192.168.1.102")
    cfg.K8sCPIPCIDR = getEnvAsInt("K8S_CP_IP_CIDR", 24)
    cfg.K8sVersion = getEnv("K8S_VERSION", "v1.35.0")
    cfg.K8sPodCIDR = getEnv("K8S_POD_CIDR", "10.244.0.0/16")
    cfg.K8sServiceCIDR = getEnv("K8S_SERVICE_CIDR", "10.96.0.0/12")

    // Virtual IP Configuration
	cfg.K8sDOMAIN = getEnv("K8S_DOMAIN", "localhost")
    cfg.K8sVIP = getEnv("K8S_VIP", "192.168.1.100")
    cfg.K8sVIPInterface = getEnv("K8S_VIP_INTERFACE", "eth0")
    cfg.K8sVIPARPEnabled = getEnvAsBool("K8S_VIP_ARP_ENABLED", true)
    cfg.K8sVIPLeaderElection = getEnvAsBool("K8S_VIP_LEADER_ELECTION", true)

    // MetalLB Configuration
    cfg.K8sMetalLBEnabled = getEnvAsBool("K8S_METALLB_ENABLED", true)
    cfg.K8sMetalLBIPRangeStart = getEnv("K8S_METALLB_IP_RANGE_START", "192.168.1.105")
    cfg.K8sMetalLBIPRangeEnd = getEnv("K8S_METALLB_IP_RANGE_END", "192.168.1.110")
    cfg.K8sMetalLBAddressPoolName = getEnv("K8S_METALLB_ADDRESS_POOL_NAME", "default-pool")

	// SSH Configuration
	// Untuk production, gunakan Pulumi ESC atau Secret Manager
    cfg.SSHUser = getEnv("SSH_USER", "ubuntu")

	// Load SSH Private Key dengan prioritas:
    // 1. Dari Pulumi Config (encrypted secret)
    // 2. Dari environment variable
    // 3. Dari file path
    cfg.SSHPrivateKey = pulumiCfg.Get("sshPrivateKey")
    if cfg.SSHPrivateKey == "" {
        cfg.SSHPrivateKey = os.Getenv("SSH_PRIVATE_KEY")
    }
    if cfg.SSHPrivateKey == "" {
		// Fallback: baca dari file jika tidak ada di Pulumi config
        keyPath := getEnv("SSH_PRIVATE_KEY_PATH", "~/.ssh/id_rsa")
        expandedPath := os.ExpandEnv(keyPath)
        if expandedPath[:2] == "~/" {
            homeDir, _ := os.UserHomeDir()
            expandedPath = homeDir + expandedPath[1:]
        }
        keyBytes, err := os.ReadFile(expandedPath)
        if err != nil {
            return nil, fmt.Errorf("❌ Failed to read SSH private key from %s: %w\n"+
                "Please set SSH key via one of these methods:\n"+
                "  1. pulumi config set --secret sshPrivateKey < ~/.ssh/id_rsa\n"+
                "  2. export SSH_PRIVATE_KEY=\"$(cat ~/.ssh/id_rsa)\"\n"+
                "  3. Set SSH_PRIVATE_KEY_PATH in .env file", expandedPath, err)
        }
        cfg.SSHPrivateKey = string(keyBytes)
    }

    // GitOps Configuration
    cfg.GitOpsRepoURL = getEnv("GITOPS_REPO_URL", "")
    cfg.GitOpsBranch = getEnv("GITOPS_BRANCH", "main")
    cfg.ArgoCDProjectPrefix = getEnv("ARGOCD_PROJECT_PREFIX", "ha-cluster-dev")

    // Stack Metadata
    cfg.PulumiStack = getEnv("PULUMI_STACK", "dev")
    cfg.PulumiBackend = getEnv("PULUMI_BACKEND", "local")

	// Environment Configuration
    cfg.Environment = getEnv("ENVIRONMENT", "development")
    cfg.MockMode = getEnvAsBool("MOCK_MODE", cfg.Environment == "development")

    // Validate configuration
    if err := cfg.Validate(); err != nil {
        return nil, fmt.Errorf("❌ Invalid configuration: %w", err)
    }

    // Print loaded configuration summary
    cfg.PrintSummary()

    return cfg, nil
}

func (c *Config) Validate() error {
    // Validate IP addresses
    if net.ParseIP(c.K8sCPIPStart) == nil {
        return fmt.Errorf("invalid K8S_CP_IP_START: %s", c.K8sCPIPStart)
    }
    if net.ParseIP(c.K8sCPIPEnd) == nil {
        return fmt.Errorf("invalid K8S_CP_IP_END: %s", c.K8sCPIPEnd)
    }
    if net.ParseIP(c.K8sVIP) == nil {
        return fmt.Errorf("invalid K8S_VIP: %s", c.K8sVIP)
    }

    // Validate CIDR
    if _, _, err := net.ParseCIDR(c.K8sPodCIDR); err != nil {
        return fmt.Errorf("invalid K8S_POD_CIDR: %s", c.K8sPodCIDR)
    }
    if _, _, err := net.ParseCIDR(c.K8sServiceCIDR); err != nil {
        return fmt.Errorf("invalid K8S_SERVICE_CIDR: %s", c.K8sServiceCIDR)
    }

    // Validate MetalLB IPs if enabled
    if c.K8sMetalLBEnabled {
        if net.ParseIP(c.K8sMetalLBIPRangeStart) == nil {
            return fmt.Errorf("invalid K8S_METALLB_IP_RANGE_START: %s", c.K8sMetalLBIPRangeStart)
        }
        if net.ParseIP(c.K8sMetalLBIPRangeEnd) == nil {
            return fmt.Errorf("invalid K8S_METALLB_IP_RANGE_END: %s", c.K8sMetalLBIPRangeEnd)
        }
    }

	// Validate environment-specific settings
    if c.Environment == "production" && c.MockMode {
        return fmt.Errorf("MOCK_MODE cannot be enabled in production environment")
    }
    
    // Warn if using mock IPs in non-development environment
    if c.MockMode && c.Environment != "development" {
        fmt.Printf("⚠️  WARNING: MOCK_MODE is enabled in %s environment\n", c.Environment)
    }

    return nil
}

// PrintSummary prints configuration summary
func (c *Config) PrintSummary() {
    fmt.Println("\n📋 Configuration Summary:")
    fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
    fmt.Printf("🌍 Environment: %s", c.Environment)
    if c.MockMode {
        fmt.Printf(" (MOCK MODE)\n")
    } else {
        fmt.Printf("\n")
    }
    fmt.Printf("🖥️  Hyper-V: %d nodes, %d CPUs, %dGB RAM\n", c.HyperVNodeCount, c.HyperVCPUs, c.HyperVMemoryGB)
    fmt.Printf("🎯 Control Plane: %s (%s - %s)\n", c.K8sCPHostnamePrefix, c.K8sCPIPStart, c.K8sCPIPEnd)
    fmt.Printf("🌐 Virtual IP: %s (%s)\n", c.K8sVIP, c.K8sVIPInterface)
    fmt.Printf("📦 Kubernetes: %s\n", c.K8sVersion)
    fmt.Printf("🔧 Pod CIDR: %s\n", c.K8sPodCIDR)
    fmt.Printf("⚖️  MetalLB: %s - %s\n", c.K8sMetalLBIPRangeStart, c.K8sMetalLBIPRangeEnd)
    fmt.Printf("🔐 SSH User: %s\n", c.SSHUser)
    fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
}

// Helper functions
func getEnv(key, defaultValue string) string {
    if value := os.Getenv(key); value != "" {
        return value
    }
    return defaultValue
}

func getEnvAsInt(key string, defaultValue int) int {
    valueStr := os.Getenv(key)
    if value, err := strconv.Atoi(valueStr); err == nil {
        return value
    }
    return defaultValue
}

func getEnvAsBool(key string, defaultValue bool) bool {
    valueStr := os.Getenv(key)
    if value, err := strconv.ParseBool(valueStr); err == nil {
        return value
    }
    return defaultValue
}

// GenerateControlPlaneIPs generates IP addresses for control plane nodes
func (c *Config) GenerateControlPlaneIPs() ([]string, error) {
    startIP := net.ParseIP(c.K8sCPIPStart)
    endIP := net.ParseIP(c.K8sCPIPEnd)

    if startIP == nil || endIP == nil {
        return nil, fmt.Errorf("invalid IP range")
    }

    var ips []string
    for ip := startIP; !ip.Equal(endIP); incrementIP(ip) {
        ips = append(ips, ip.String())
    }
    ips = append(ips, endIP.String())

    return ips, nil
}

func incrementIP(ip net.IP) {
    for j := len(ip) - 1; j >= 0; j-- {
        ip[j]++
        if ip[j] > 0 {
            break
        }
    }
}