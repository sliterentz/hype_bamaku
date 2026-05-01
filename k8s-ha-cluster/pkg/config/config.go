package config

import (
    "fmt"
    "net"
    "os"
    "strconv"
    "encoding/base64"

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
	SSHPrivateKey     pulumi.StringInput
    SSHPrivateKeyPath string

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
    // 1. Load .env file dengan absolute/cwd path agar terbaca saat pulumi up/preview
    cwd, _ := os.Getwd()
    envPath := cwd + "/.env"
    if customPath := os.Getenv("ENV_FILE_PATH"); customPath != "" {
        envPath = customPath
    }

    if err := godotenv.Load(envPath); err != nil {
        if os.IsNotExist(err) {
            fmt.Printf("ℹ️  Info: %s not found, using Pulumi Config, environment variables, or defaults\n", envPath)
        } else {
            return nil, fmt.Errorf("failed to load %s: %w", envPath, err)
        }
    } else {
        fmt.Printf("✅ Loaded configuration from %s\n", envPath)
    }

    cfg := &Config{}
    pulumiCfg := pulumiConfig.New(ctx, "")

    // Fungsi helper untuk membaca Config dengan urutan prioritas:
    // 1. Pulumi Config
    // 2. Environment Variable (dari .env atau OS)
    // 3. Default Value
    getConfigStr := func(configKey, envKey, defaultVal string) string {
        if val := pulumiCfg.Get(configKey); val != "" {
            return val
        }
        if val := os.Getenv(envKey); val != "" {
            return val
        }
        return defaultVal
    }

    getConfigInt := func(configKey, envKey string, defaultVal int) int {
        if valStr := pulumiCfg.Get(configKey); valStr != "" {
            if val, err := strconv.Atoi(valStr); err == nil {
                return val
            }
        }
        if valStr := os.Getenv(envKey); valStr != "" {
            if val, err := strconv.Atoi(valStr); err == nil {
                return val
            }
        }
        return defaultVal
    }

    getConfigBool := func(configKey, envKey string, defaultVal bool) bool {
        if valStr := pulumiCfg.Get(configKey); valStr != "" {
            if val, err := strconv.ParseBool(valStr); err == nil {
                return val
            }
        }
        if valStr := os.Getenv(envKey); valStr != "" {
            if val, err := strconv.ParseBool(valStr); err == nil {
                return val
            }
        }
        return defaultVal
    }

    // Hyper-V Configuration
    cfg.HyperVNodeCount = getConfigInt("hypervNodeCount", "HYPERV_NODE_COUNT", 2)
    cfg.HyperVCPUs = getConfigInt("hypervCpus", "HYPERV_CPUS", 4)
    cfg.HyperVMemoryGB = getConfigInt("hypervMemoryGB", "HYPERV_MEMORY_GB", 8)
    cfg.HyperVDiskGB = getConfigInt("hypervDiskGB", "HYPERV_DISK_GB", 50)
    cfg.HyperVNetworkSwitch = getConfigStr("hypervNetworkSwitch", "HYPERV_NETWORK_SWITCH", "DefaultSwitch")
    cfg.HyperVImageURL = getConfigStr("hypervImageURL", "HYPERV_IMAGE_URL", "")

    // Kubernetes Control Plane Configuration
    cfg.K8sCPHostnamePrefix = getConfigStr("k8sCPHostnamePrefix", "K8S_CP_HOSTNAME_PREFIX", "k8s-ha-cp")
    cfg.K8sCPIPStart = getConfigStr("k8sCPIPStart", "K8S_CP_IP_START", "192.168.1.101")
    cfg.K8sCPIPEnd = getConfigStr("k8sCPIPEnd", "K8S_CP_IP_END", "192.168.1.102")
    cfg.K8sCPIPCIDR = getConfigInt("k8sCPIPCIDR", "K8S_CP_IP_CIDR", 24)
    cfg.K8sVersion = getConfigStr("k8sVersion", "K8S_VERSION", "v1.35.0")
    cfg.K8sPodCIDR = getConfigStr("k8sPodCIDR", "K8S_POD_CIDR", "10.244.0.0/16")
    cfg.K8sServiceCIDR = getConfigStr("k8sServiceCIDR", "K8S_SERVICE_CIDR", "10.96.0.0/12")

    // Virtual IP Configuration
    cfg.K8sDOMAIN = getConfigStr("k8sDomain", "K8S_VIP_DOMAIN", "localhost")
    cfg.K8sVIP = getConfigStr("controlPlaneVIP", "K8S_VIP", "192.168.1.100")
    cfg.K8sVIPInterface = getConfigStr("k8sVIPInterface", "K8S_VIP_INTERFACE", "eth0")
    cfg.K8sVIPARPEnabled = getConfigBool("k8sVIPARPEnabled", "K8S_VIP_ARP_ENABLED", true)
    cfg.K8sVIPLeaderElection = getConfigBool("k8sVIPLeaderElection", "K8S_VIP_LEADER_ELECTION", true)

    // MetalLB Configuration
    cfg.K8sMetalLBEnabled = getConfigBool("k8sMetalLBEnabled", "K8S_METALLB_ENABLED", true)
    cfg.K8sMetalLBIPRangeStart = getConfigStr("metallbStart", "K8S_METALLB_IP_RANGE_START", "192.168.1.105")
    cfg.K8sMetalLBIPRangeEnd = getConfigStr("metallbEnd", "K8S_METALLB_IP_RANGE_END", "192.168.1.110")
    cfg.K8sMetalLBAddressPoolName = getConfigStr("k8sMetalLBAddressPoolName", "K8S_METALLB_ADDRESS_POOL_NAME", "default-pool")

    // SSH Configuration
    cfg.SSHUser = getConfigStr("sshUser", "SSH_USER", "ubuntu")
    cfg.SSHPrivateKeyPath = getConfigStr("sshPrivateKeyPath", "SSH_PRIVATE_KEY_PATH", "~/.ssh/id_rsa")

    // Load SSH Private Key dengan prioritas:
    // 1. Dari Pulumi Config (encrypted secret)
    // 2. Dari environment variable
    // 3. Dari file path
    sshKeyLoaded := false

    // Coba baca dari Pulumi Config (Get() bisa membaca plain text maupun secret yang sudah di-decrypt)
    if sshKey, err := pulumiCfg.TrySecret("sshPrivateKey"); err == nil {
        cfg.SSHPrivateKey = sshKey
        sshKeyLoaded = true
        fmt.Println("✅ SSH Private Key loaded from Pulumi Config")
    }

    // Jika tidak ada di Pulumi Secret, coba environment variable
    if !sshKeyLoaded {
        if envKey := os.Getenv("SSH_PRIVATE_KEY"); envKey != "" {
            
            // Decode SSH private key dari base64
            sshKeyBytes, err := base64.StdEncoding.DecodeString(envKey)
            if err != nil {
                return nil, fmt.Errorf("failed to decode SSH private key: %w", err)
            }
            cfg.SSHPrivateKey = pulumi.ToSecret(pulumi.String(string(sshKeyBytes))).(pulumi.StringInput)
            // cfg.SSHPrivateKey = envKey
            sshKeyLoaded = true
            fmt.Println("✅ SSH Private Key loaded from Environment Variable")
        }
    }

    // Jika masih belum ada, coba baca dari file
    if !sshKeyLoaded {
        keyPath := getConfigStr("sshPrivateKeyPath", "SSH_PRIVATE_KEY_PATH", "~/.ssh/id_rsa")
        expandedPath := os.ExpandEnv(keyPath)
        
        // Handle tilde (~) expansion
        if len(expandedPath) >= 2 && expandedPath[:2] == "~/" {
            homeDir, err := os.UserHomeDir()
            if err == nil {
                expandedPath = homeDir + expandedPath[1:]
            }
        }
        
        if keyBytes, err := os.ReadFile(expandedPath); err == nil {
            cfg.SSHPrivateKey = pulumi.ToSecret(pulumi.String(string(keyBytes))).(pulumi.StringInput)
            sshKeyLoaded = true
            fmt.Printf("✅ SSH Private Key loaded from file: %s\n", expandedPath)
        } else {
            fmt.Printf("⚠️  Warning: Failed to read SSH private key from %s: %v\n", expandedPath, err)
        }
    }

    // Validasi apakah SSH key berhasil dimuat
    if !sshKeyLoaded || cfg.SSHPrivateKey == nil {
        fmt.Println("⚠️  WARNING: No SSH private key configured. Remote operations may fail.")
        fmt.Println("   Set it using one of these methods:")
        fmt.Println("   1. pulumi config set --secret sshPrivateKey < ~/.ssh/your_key")
        fmt.Println("   2. export SSH_PRIVATE_KEY=\"$(cat ~/.ssh/your_key)\"")
        fmt.Println("   3. Set SSH_PRIVATE_KEY_PATH in .env file")
    }

    // GitOps Configuration
    cfg.GitOpsRepoURL = getConfigStr("gitOpsRepoURL", "GITOPS_REPO_URL", "")
    cfg.GitOpsBranch = getConfigStr("gitOpsBranch", "GITOPS_BRANCH", "main")
    cfg.ArgoCDProjectPrefix = getConfigStr("argoCDProjectPrefix", "ARGOCD_PROJECT_PREFIX", "ha-cluster-dev")

    // Stack Metadata
    cfg.PulumiStack = getConfigStr("pulumiStack", "PULUMI_STACK", "dev")
    cfg.PulumiBackend = getConfigStr("pulumiBackend", "PULUMI_BACKEND", "local")

    // Environment Configuration
    cfg.Environment = getConfigStr("environment", "ENVIRONMENT", "development")
    cfg.MockMode = getConfigBool("mockMode", "MOCK_MODE", cfg.Environment == "development")

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
    fmt.Printf("🔗 VIP Domain: %s\n", c.K8sDOMAIN)
    fmt.Printf("📦 Kubernetes: %s\n", c.K8sVersion)
    fmt.Printf("🔧 Pod CIDR: %s\n", c.K8sPodCIDR)
    fmt.Printf("⚖️ MetalLB: %s - %s\n", c.K8sMetalLBIPRangeStart, c.K8sMetalLBIPRangeEnd)
    fmt.Printf("🔐 SSH User: %s\n", c.SSHUser)
    fmt.Printf("🔐 SSH Private Key Path: %s\n", c.SSHPrivateKeyPath)
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