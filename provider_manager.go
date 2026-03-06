package platform

import (
	"context"
	"fmt"
	"sync"

	"github.com/zzliekkas/flow-platform/providers"
)

// ProviderManager 云服务提供商管理器
type ProviderManager struct {
	// 已注册的提供商
	providers map[string]providers.Provider

	// 互斥锁
	mu sync.RWMutex
}

// NewProviderManager 创建云服务提供商管理器
func NewProviderManager() *ProviderManager {
	return &ProviderManager{
		providers: make(map[string]providers.Provider),
	}
}

// RegisterProvider 注册云服务提供商
func (pm *ProviderManager) RegisterProvider(provider providers.Provider) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	name := provider.Provider()
	if name == "" {
		return fmt.Errorf("提供商名称不能为空")
	}

	if _, exists := pm.providers[name]; exists {
		return fmt.Errorf("提供商 '%s' 已经注册", name)
	}

	pm.providers[name] = provider
	return nil
}

// GetProvider 获取云服务提供商
func (pm *ProviderManager) GetProvider(name string) (providers.Provider, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	provider, exists := pm.providers[name]
	if !exists {
		return nil, fmt.Errorf("提供商 '%s' 不存在", name)
	}

	return provider, nil
}

// HasProvider 检查是否存在指定的云服务提供商
func (pm *ProviderManager) HasProvider(name string) bool {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	_, exists := pm.providers[name]
	return exists
}

// GetProviderNames 获取所有已注册的云服务提供商名称
func (pm *ProviderManager) GetProviderNames() []string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	names := make([]string, 0, len(pm.providers))
	for name := range pm.providers {
		names = append(names, name)
	}

	return names
}

// InitProviders 初始化所有云服务提供商
func (pm *ProviderManager) InitProviders(ctx context.Context) error {
	pm.mu.RLock()
	providers := make([]providers.Provider, 0, len(pm.providers))
	for _, provider := range pm.providers {
		providers = append(providers, provider)
	}
	pm.mu.RUnlock()

	for _, provider := range providers {
		if err := provider.Init(ctx); err != nil {
			return fmt.Errorf("初始化提供商 '%s' 失败: %w", provider.Provider(), err)
		}
	}

	return nil
}

// CheckHealthAll 检查所有云服务提供商的健康状态
func (pm *ProviderManager) CheckHealthAll(ctx context.Context) map[string]error {
	pm.mu.RLock()
	providers := make(map[string]providers.Provider, len(pm.providers))
	for name, provider := range pm.providers {
		providers[name] = provider
	}
	pm.mu.RUnlock()

	results := make(map[string]error, len(providers))
	for name, provider := range providers {
		err := provider.CheckConnectionHealth(ctx)
		results[name] = err
	}

	return results
}

// TypedProvider 泛型函数，获取特定类型的提供商
func TypedProvider[T any](pm *ProviderManager, name string) (T, error) {
	var zero T
	provider, err := pm.GetProvider(name)
	if err != nil {
		return zero, err
	}

	typed, ok := provider.(T)
	if !ok {
		return zero, fmt.Errorf("提供商 '%s' 不是请求的类型", name)
	}

	return typed, nil
}
