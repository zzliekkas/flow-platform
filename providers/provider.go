package providers

import (
	"context"
)

// Provider 云服务提供商接口
type Provider interface {
	// Provider 返回提供商名称
	Provider() string

	// Init 初始化提供商
	Init(ctx context.Context) error

	// CheckConnectionHealth 检查连接健康状态
	CheckConnectionHealth(ctx context.Context) error
}
