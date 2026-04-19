package platform

import (
	"time"

	"github.com/zzliekkas/flow/v3"
)

// PlatformModule 将 flow-platform 的核心能力以 flow.Module 形式接入 Flow Engine。
//
// 默认会注册：
//   - *ContainerAdapter：容器环境检测
//   - *ProviderManager：云服务提供商管理器
//   - *GracefulManager：优雅启停管理器
//   - *HealthChecker（当 Options.EnableHealth 为 true 时）
//
// 可通过选项进一步定制：自定义容器配置、关闭超时、以及是否启用健康检查。
type PlatformModule struct {
	opts Options
}

// Options 控制 PlatformModule 注册哪些能力。
type Options struct {
	// Container 指定容器适配器的初始配置；为空时使用 DefaultContainerConfig()
	Container ContainerConfig

	// ShutdownTimeout 优雅关闭超时；为 0 时使用 30s
	ShutdownTimeout time.Duration

	// EnableHealth 是否向 DI 容器注册 *HealthChecker
	EnableHealth bool

	// HookEngineShutdown 是否把 GracefulManager 的 Shutdown 挂到 Engine.OnShutdown
	HookEngineShutdown bool
}

// ModuleOption 配置 PlatformModule。
type ModuleOption func(*Options)

// WithContainerConfig 自定义容器配置。
func WithContainerConfig(cfg ContainerConfig) ModuleOption {
	return func(o *Options) { o.Container = cfg }
}

// WithShutdownTimeout 自定义优雅关闭超时。
func WithShutdownTimeout(d time.Duration) ModuleOption {
	return func(o *Options) { o.ShutdownTimeout = d }
}

// WithHealthChecker 注册 *HealthChecker 到 DI 容器。
func WithHealthChecker() ModuleOption {
	return func(o *Options) { o.EnableHealth = true }
}

// WithEngineShutdownHook 将 GracefulManager 绑定到 Engine.OnShutdown。
func WithEngineShutdownHook() ModuleOption {
	return func(o *Options) { o.HookEngineShutdown = true }
}

// NewModule 创建新的 PlatformModule。
func NewModule(opts ...ModuleOption) *PlatformModule {
	m := &PlatformModule{}
	for _, opt := range opts {
		opt(&m.opts)
	}
	return m
}

// Name 返回模块名。
func (m *PlatformModule) Name() string {
	return "platform"
}

// Init 将 platform 能力注册到 Flow 的 DI 容器。
func (m *PlatformModule) Init(e *flow.Engine) error {
	containerCfg := m.opts.Container
	if containerCfg.WorkDir == "" {
		containerCfg = DefaultContainerConfig()
	}
	adapter := NewContainerAdapter(containerCfg)
	if err := e.Provide(func() *ContainerAdapter { return adapter }); err != nil {
		return err
	}

	providerMgr := NewProviderManager()
	if err := e.Provide(func() *ProviderManager { return providerMgr }); err != nil {
		return err
	}

	timeout := m.opts.ShutdownTimeout
	if timeout <= 0 {
		timeout = adapter.GetShutdownTimeout()
	}
	graceful := NewGracefulManager(timeout)
	if err := e.Provide(func() *GracefulManager { return graceful }); err != nil {
		return err
	}

	if m.opts.EnableHealth {
		hm := NewHealthManager()
		if err := e.Provide(func() *HealthManager { return hm }); err != nil {
			return err
		}
	}

	if m.opts.HookEngineShutdown {
		e.OnShutdown(func() {
			_ = providerMgr // 预留：后续可在此关闭 providers
			graceful.Shutdown()
		})
	}

	return nil
}
