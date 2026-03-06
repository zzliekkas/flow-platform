package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// HealthStatus 定义了健康状态类型
type HealthStatus string

const (
	// StatusHealthy 表示组件健康
	StatusHealthy HealthStatus = "healthy"

	// StatusUnhealthy 表示组件不健康
	StatusUnhealthy HealthStatus = "unhealthy"

	// StatusDegraded 表示组件性能下降但仍可工作
	StatusDegraded HealthStatus = "degraded"

	// StatusStarting 表示组件正在启动
	StatusStarting HealthStatus = "starting"

	// StatusStopping 表示组件正在停止
	StatusStopping HealthStatus = "stopping"
)

// HealthCheck 定义健康检查函数类型
type HealthCheck func(ctx context.Context) (HealthStatus, error)

// ComponentHealth 组件健康状态
type ComponentHealth struct {
	// 组件名称
	Name string `json:"name"`

	// 组件状态
	Status HealthStatus `json:"status"`

	// 上次检查时间
	LastCheck time.Time `json:"last_check"`

	// 错误信息（如果有）
	Error string `json:"error,omitempty"`

	// 连续失败次数
	FailureCount int `json:"failure_count,omitempty"`

	// 连续成功次数
	SuccessCount int `json:"success_count,omitempty"`

	// 额外信息
	Details map[string]interface{} `json:"details,omitempty"`
}

// HealthCheckConfig 健康检查配置
type HealthCheckConfig struct {
	// 检查间隔
	Interval time.Duration

	// 超时时间
	Timeout time.Duration

	// 初始延迟（应用启动后多久开始检查）
	InitialDelay time.Duration

	// 失败阈值（连续失败多少次认为组件不健康）
	FailureThreshold int

	// 成功阈值（连续成功多少次认为组件恢复健康）
	SuccessThreshold int
}

// DefaultHealthCheckConfig 返回默认健康检查配置
func DefaultHealthCheckConfig() HealthCheckConfig {
	return HealthCheckConfig{
		Interval:         time.Second * 10,
		Timeout:          time.Second * 5,
		InitialDelay:     time.Second * 5,
		FailureThreshold: 3,
		SuccessThreshold: 1,
	}
}

// HealthManager 健康检查管理器
type HealthManager struct {
	// 组件健康状态
	components map[string]*ComponentHealth

	// 健康检查函数
	checks map[string]HealthCheck

	// 组件配置
	configs map[string]HealthCheckConfig

	// 互斥锁
	mu sync.RWMutex

	// HTTP处理函数
	httpHandler http.Handler

	// 健康检查是否启动
	started bool

	// 上下文和取消函数
	ctx    context.Context
	cancel context.CancelFunc
}

// NewHealthManager 创建健康检查管理器
func NewHealthManager() *HealthManager {
	ctx, cancel := context.WithCancel(context.Background())

	hm := &HealthManager{
		components: make(map[string]*ComponentHealth),
		checks:     make(map[string]HealthCheck),
		configs:    make(map[string]HealthCheckConfig),
		ctx:        ctx,
		cancel:     cancel,
	}

	// 创建HTTP处理函数
	hm.httpHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hm.ServeHTTP(w, r)
	})

	return hm
}

// RegisterCheck 注册健康检查
func (hm *HealthManager) RegisterCheck(name string, check HealthCheck, config ...HealthCheckConfig) {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	// 使用默认配置或自定义配置
	var cfg HealthCheckConfig
	if len(config) > 0 {
		cfg = config[0]
	} else {
		cfg = DefaultHealthCheckConfig()
	}

	hm.checks[name] = check
	hm.configs[name] = cfg

	// 初始化组件状态为启动中
	hm.components[name] = &ComponentHealth{
		Name:      name,
		Status:    StatusStarting,
		LastCheck: time.Now(),
	}
}

// UnregisterCheck 注销健康检查
func (hm *HealthManager) UnregisterCheck(name string) {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	delete(hm.checks, name)
	delete(hm.configs, name)
	delete(hm.components, name)
}

// Start 启动健康检查
func (hm *HealthManager) Start() {
	hm.mu.Lock()
	if hm.started {
		hm.mu.Unlock()
		return
	}
	hm.started = true
	hm.mu.Unlock()

	// 启动所有组件的健康检查
	for name, check := range hm.checks {
		go hm.startCheckLoop(name, check)
	}
}

// Stop 停止健康检查
func (hm *HealthManager) Stop() {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	if !hm.started {
		return
	}

	// 取消上下文
	hm.cancel()
	hm.started = false
}

// GetHealth 获取特定组件健康状态
func (hm *HealthManager) GetHealth(name string) *ComponentHealth {
	hm.mu.RLock()
	defer hm.mu.RUnlock()

	if health, ok := hm.components[name]; ok {
		return health
	}

	return nil
}

// GetAllHealth 获取所有组件健康状态
func (hm *HealthManager) GetAllHealth() map[string]*ComponentHealth {
	hm.mu.RLock()
	defer hm.mu.RUnlock()

	// 创建副本
	result := make(map[string]*ComponentHealth, len(hm.components))
	for name, health := range hm.components {
		copiedHealth := *health
		result[name] = &copiedHealth
	}

	return result
}

// IsHealthy 检查特定组件是否健康
func (hm *HealthManager) IsHealthy(name string) bool {
	hm.mu.RLock()
	defer hm.mu.RUnlock()

	if health, ok := hm.components[name]; ok {
		return health.Status == StatusHealthy
	}

	return false
}

// IsSystemHealthy 检查整个系统是否健康
func (hm *HealthManager) IsSystemHealthy() bool {
	hm.mu.RLock()
	defer hm.mu.RUnlock()

	for _, health := range hm.components {
		if health.Status != StatusHealthy {
			return false
		}
	}

	return len(hm.components) > 0
}

// ServeHTTP 实现HTTP处理函数
func (hm *HealthManager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 获取路径
	path := r.URL.Path

	// 设置内容类型
	w.Header().Set("Content-Type", "application/json")

	switch path {
	case "/health", "/healthz":
		// 检查整体健康状态
		if hm.IsSystemHealthy() {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}

		// 返回简单状态
		status := "healthy"
		if !hm.IsSystemHealthy() {
			status = "unhealthy"
		}

		json.NewEncoder(w).Encode(map[string]string{
			"status": status,
		})

	case "/health/details", "/healthz/details":
		// 返回详细状态
		allHealth := hm.GetAllHealth()

		// 确定HTTP状态码
		statusCode := http.StatusOK
		if !hm.IsSystemHealthy() {
			statusCode = http.StatusServiceUnavailable
		}

		w.WriteHeader(statusCode)
		json.NewEncoder(w).Encode(allHealth)

	default:
		// 对于特定组件的健康状态
		componentName := ""
		if len(path) > 8 && path[:8] == "/health/" {
			componentName = path[8:]
		} else if len(path) > 9 && path[:9] == "/healthz/" {
			componentName = path[9:]
		}

		if componentName != "" && componentName != "details" {
			health := hm.GetHealth(componentName)
			if health != nil {
				if health.Status == StatusHealthy {
					w.WriteHeader(http.StatusOK)
				} else {
					w.WriteHeader(http.StatusServiceUnavailable)
				}
				json.NewEncoder(w).Encode(health)
			} else {
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(map[string]string{
					"error": fmt.Sprintf("component %s not found", componentName),
				})
			}
		} else {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "health check endpoint not found",
			})
		}
	}
}

// Handler 返回HTTP处理函数
func (hm *HealthManager) Handler() http.Handler {
	return hm.httpHandler
}

// RegisterHandlers 将健康检查处理函数注册到HTTP服务器
func (hm *HealthManager) RegisterHandlers(mux *http.ServeMux) {
	mux.Handle("/health", hm.httpHandler)
	mux.Handle("/health/", hm.httpHandler)
	mux.Handle("/healthz", hm.httpHandler)
	mux.Handle("/healthz/", hm.httpHandler)
}

// 内部方法：开始健康检查循环
func (hm *HealthManager) startCheckLoop(name string, check HealthCheck) {
	config := hm.configs[name]

	// 应用初始延迟
	select {
	case <-time.After(config.InitialDelay):
	case <-hm.ctx.Done():
		return
	}

	// 定时执行健康检查
	ticker := time.NewTicker(config.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			hm.performCheck(name, check, config)
		case <-hm.ctx.Done():
			return
		}
	}
}

// 内部方法：执行健康检查
func (hm *HealthManager) performCheck(name string, check HealthCheck, config HealthCheckConfig) {
	// 创建带超时的上下文
	ctx, cancel := context.WithTimeout(hm.ctx, config.Timeout)
	defer cancel()

	// 执行检查
	status, err := check(ctx)

	// 更新组件状态
	hm.mu.Lock()
	defer hm.mu.Unlock()

	health, ok := hm.components[name]
	if !ok {
		return
	}

	health.LastCheck = time.Now()

	if err != nil || status != StatusHealthy {
		health.FailureCount++
		health.SuccessCount = 0
		if err != nil {
			health.Error = err.Error()
		} else {
			health.Error = ""
		}

		// 根据失败阈值更新状态
		if health.FailureCount >= config.FailureThreshold {
			health.Status = status
		}
	} else {
		health.SuccessCount++
		health.FailureCount = 0
		health.Error = ""

		// 根据成功阈值更新状态
		if health.SuccessCount >= config.SuccessThreshold {
			health.Status = StatusHealthy
		}
	}
}
