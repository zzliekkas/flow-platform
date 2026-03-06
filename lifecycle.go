package platform

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// ShutdownCallback 定义关闭回调函数类型
type ShutdownCallback func(ctx context.Context) error

// LifecycleManager 生命周期管理器
type LifecycleManager struct {
	// 互斥锁
	mu sync.Mutex

	// 关闭回调
	shutdownCallbacks []ShutdownCallback

	// 优雅关闭超时
	shutdownTimeout time.Duration

	// 是否已启动
	started bool

	// 是否已关闭
	shutdown bool

	// 关闭信号通道
	shutdownCh chan struct{}

	// 完成关闭通道
	doneCh chan struct{}

	// 监听系统信号
	signalCh chan os.Signal
}

// NewLifecycleManager 创建生命周期管理器
func NewLifecycleManager(timeout time.Duration) *LifecycleManager {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	return &LifecycleManager{
		shutdownCallbacks: make([]ShutdownCallback, 0),
		shutdownTimeout:   timeout,
		shutdownCh:        make(chan struct{}),
		doneCh:            make(chan struct{}),
		signalCh:          make(chan os.Signal, 1),
	}
}

// AddShutdownCallback 添加关闭回调
func (lm *LifecycleManager) AddShutdownCallback(callback ShutdownCallback) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	lm.shutdownCallbacks = append(lm.shutdownCallbacks, callback)
}

// Start 启动生命周期管理
func (lm *LifecycleManager) Start() {
	lm.mu.Lock()
	if lm.started {
		lm.mu.Unlock()
		return
	}
	lm.started = true
	lm.mu.Unlock()

	// 监听系统信号
	signal.Notify(lm.signalCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	// 开始监听关闭信号
	go lm.waitForShutdown()
}

// Shutdown 触发优雅关闭
func (lm *LifecycleManager) Shutdown() {
	lm.mu.Lock()
	if lm.shutdown {
		lm.mu.Unlock()
		return
	}
	lm.shutdown = true
	lm.mu.Unlock()

	// 触发关闭信号
	close(lm.shutdownCh)

	// 等待关闭完成
	<-lm.doneCh
}

// ShutdownContext 创建关闭上下文
func (lm *LifecycleManager) ShutdownContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), lm.shutdownTimeout)
}

// WaitForShutdown 等待关闭信号
func (lm *LifecycleManager) WaitForShutdown() {
	<-lm.shutdownCh
}

// SetShutdownTimeout 设置关闭超时
func (lm *LifecycleManager) SetShutdownTimeout(timeout time.Duration) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	if timeout > 0 {
		lm.shutdownTimeout = timeout
	}
}

// 内部方法：等待关闭信号
func (lm *LifecycleManager) waitForShutdown() {
	// 等待关闭信号或系统信号
	select {
	case <-lm.shutdownCh:
		// 主动调用关闭
	case sig := <-lm.signalCh:
		// 收到系统信号
		if sig == syscall.SIGHUP {
			// 对于SIGHUP，某些应用可能希望重新加载而不是关闭
			// 但我们这里简单处理为关闭
		}

		// 标记为关闭状态
		lm.mu.Lock()
		lm.shutdown = true
		lm.mu.Unlock()
	}

	// 不再接收更多信号
	signal.Stop(lm.signalCh)

	// 创建带超时的上下文
	ctx, cancel := lm.ShutdownContext()
	defer cancel()

	// 执行所有关闭回调
	var wg sync.WaitGroup
	lm.mu.Lock()
	callbacks := append([]ShutdownCallback{}, lm.shutdownCallbacks...)
	lm.mu.Unlock()

	for _, callback := range callbacks {
		wg.Add(1)
		go func(cb ShutdownCallback) {
			defer wg.Done()
			_ = cb(ctx)
		}(callback)
	}

	// 等待所有回调完成或超时
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// 所有回调正常完成
	case <-ctx.Done():
		// 超时
	}

	// 标记关闭完成
	close(lm.doneCh)
}

// GracefulServer 支持优雅启动和关闭的服务器接口
type GracefulServer interface {
	// Start 启动服务器
	Start() error

	// Stop 停止服务器
	Stop(ctx context.Context) error
}

// GracefulManager 优雅启动和关闭管理器
type GracefulManager struct {
	// 生命周期管理器
	lifecycle *LifecycleManager

	// 服务器列表
	servers []GracefulServer

	// 互斥锁
	mu sync.Mutex
}

// NewGracefulManager 创建优雅启动和关闭管理器
func NewGracefulManager(timeout time.Duration) *GracefulManager {
	return &GracefulManager{
		lifecycle: NewLifecycleManager(timeout),
		servers:   make([]GracefulServer, 0),
	}
}

// AddServer 添加服务器
func (gm *GracefulManager) AddServer(server GracefulServer) {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	gm.servers = append(gm.servers, server)
}

// Start 启动所有服务器
func (gm *GracefulManager) Start() error {
	gm.mu.Lock()
	servers := append([]GracefulServer{}, gm.servers...)
	gm.mu.Unlock()

	// 添加服务器关闭回调
	for _, server := range servers {
		s := server // 创建副本避免闭包问题
		gm.lifecycle.AddShutdownCallback(func(ctx context.Context) error {
			return s.Stop(ctx)
		})
	}

	// 启动生命周期管理
	gm.lifecycle.Start()

	// 启动所有服务器
	for _, server := range servers {
		if err := server.Start(); err != nil {
			return err
		}
	}

	// 等待关闭信号
	gm.lifecycle.WaitForShutdown()

	return nil
}

// Shutdown 优雅关闭所有服务器
func (gm *GracefulManager) Shutdown() {
	gm.lifecycle.Shutdown()
}

// CustomShutdownHandler 定义自定义关闭处理函数
func (gm *GracefulManager) CustomShutdownHandler(handler ShutdownCallback) {
	gm.lifecycle.AddShutdownCallback(handler)
}

// SetShutdownTimeout 设置关闭超时
func (gm *GracefulManager) SetShutdownTimeout(timeout time.Duration) {
	gm.lifecycle.SetShutdownTimeout(timeout)
}
