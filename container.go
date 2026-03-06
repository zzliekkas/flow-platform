package platform

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// ContainerConfig 容器化环境配置
type ContainerConfig struct {
	// 是否在容器环境中运行
	InContainer bool

	// 容器ID，通常是Docker容器ID或K8s Pod名称
	ContainerID string

	// 工作目录
	WorkDir string

	// 容器平台（docker, k8s, ecs等）
	Platform string

	// 环境名称（dev, test, prod等）
	Environment string

	// 自动检测到的内存限制（字节）
	MemoryLimit int64

	// 自动检测到的CPU限制（核心数）
	CPULimit float64

	// 优雅关闭超时（秒）
	ShutdownTimeout int
}

// DefaultContainerConfig 返回默认的容器配置
func DefaultContainerConfig() ContainerConfig {
	return ContainerConfig{
		InContainer:     false,
		WorkDir:         "/app",
		Environment:     "production",
		ShutdownTimeout: 30,
	}
}

// ContainerAdapter 容器环境适配器
type ContainerAdapter struct {
	// 容器配置
	config ContainerConfig
}

// NewContainerAdapter 创建容器适配器
func NewContainerAdapter(config ContainerConfig) *ContainerAdapter {
	// 如果配置为空，使用默认配置
	if config.WorkDir == "" {
		config = DefaultContainerConfig()
	}

	// 自动检测是否在容器环境中
	if !config.InContainer {
		config.InContainer = detectContainer()
	}

	// 自动检测容器ID
	if config.InContainer && config.ContainerID == "" {
		config.ContainerID = detectContainerID()
	}

	// 自动检测平台
	if config.Platform == "" {
		config.Platform = detectPlatform()
	}

	// 自动检测资源限制
	if config.InContainer {
		config.MemoryLimit = detectMemoryLimit()
		config.CPULimit = detectCPULimit()
	}

	return &ContainerAdapter{
		config: config,
	}
}

// GetConfig 获取容器配置
func (ca *ContainerAdapter) GetConfig() ContainerConfig {
	return ca.config
}

// IsInContainer 检查是否在容器环境中运行
func (ca *ContainerAdapter) IsInContainer() bool {
	return ca.config.InContainer
}

// GetEnvironment 获取当前环境名称
func (ca *ContainerAdapter) GetEnvironment() string {
	return ca.config.Environment
}

// GetShutdownTimeout 获取优雅关闭超时时间
func (ca *ContainerAdapter) GetShutdownTimeout() time.Duration {
	return time.Duration(ca.config.ShutdownTimeout) * time.Second
}

// GetMemoryLimit 获取内存限制
func (ca *ContainerAdapter) GetMemoryLimit() int64 {
	return ca.config.MemoryLimit
}

// GetCPULimit 获取CPU限制
func (ca *ContainerAdapter) GetCPULimit() float64 {
	return ca.config.CPULimit
}

// GetWorkDir 获取工作目录
func (ca *ContainerAdapter) GetWorkDir() string {
	return ca.config.WorkDir
}

// GetContainerID 获取容器ID
func (ca *ContainerAdapter) GetContainerID() string {
	return ca.config.ContainerID
}

// detectContainer 检测是否在容器环境中运行
func detectContainer() bool {
	// 检查是否存在Docker环境的标志文件
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}

	// 检查cgroup
	if data, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		return strings.Contains(string(data), "docker") ||
			strings.Contains(string(data), "kubepods") ||
			strings.Contains(string(data), "ecs")
	}

	return false
}

// detectContainerID 检测容器ID
func detectContainerID() string {
	// 尝试从主机名获取（对K8s和Docker都有效）
	if hostname, err := os.Hostname(); err == nil {
		return hostname
	}

	// 从cgroup文件尝试获取Docker ID
	if data, err := os.ReadFile("/proc/self/cgroup"); err == nil {
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			if strings.Contains(line, "docker") {
				parts := strings.Split(line, "/")
				if len(parts) > 0 {
					return parts[len(parts)-1]
				}
			}
		}
	}

	return ""
}

// detectPlatform 检测容器平台
func detectPlatform() string {
	// 检查K8s特定环境变量
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return "kubernetes"
	}

	// 检查ECS特定环境变量
	if os.Getenv("ECS_CONTAINER_METADATA_URI") != "" {
		return "ecs"
	}

	// 检查cgroup判断是否为Docker
	if data, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		if strings.Contains(string(data), "docker") {
			return "docker"
		}
	}

	return "unknown"
}

// detectMemoryLimit 检测内存限制
func detectMemoryLimit() int64 {
	// 对于K8s和Docker，可以从cgroup中读取内存限制
	if data, err := os.ReadFile("/sys/fs/cgroup/memory/memory.limit_in_bytes"); err == nil {
		limit, _ := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
		return limit
	}

	// 如果无法读取，返回0表示未知
	return 0
}

// detectCPULimit 检测CPU限制
func detectCPULimit() float64 {
	// 从cgroup读取CPU配额
	if quota, err := os.ReadFile("/sys/fs/cgroup/cpu/cpu.cfs_quota_us"); err == nil {
		if period, err := os.ReadFile("/sys/fs/cgroup/cpu/cpu.cfs_period_us"); err == nil {
			quotaVal, _ := strconv.ParseFloat(strings.TrimSpace(string(quota)), 64)
			periodVal, _ := strconv.ParseFloat(strings.TrimSpace(string(period)), 64)
			if periodVal > 0 {
				return quotaVal / periodVal
			}
		}
	}

	// 如果无法读取，返回0表示未知
	return 0
}
