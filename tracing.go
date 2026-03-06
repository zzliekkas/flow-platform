package platform

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/jaeger"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.12.0"
	"go.opentelemetry.io/otel/trace"
)

// TracerProvider 类型
type TracerProvider = trace.TracerProvider

// Tracer 类型
type Tracer = trace.Tracer

// TracerConfig 追踪配置
type TracerConfig struct {
	// 服务名称
	ServiceName string

	// 服务版本
	ServiceVersion string

	// 服务环境（如：production, staging, development）
	Environment string

	// 导出器类型：stdout, jaeger, otlp
	ExporterType string

	// 采样率 (0.0-1.0)
	SamplingRate float64

	// Jaeger端点 (如: http://jaeger:14268/api/traces)
	JaegerEndpoint string

	// OTLP端点 (如: localhost:4317)
	OTLPEndpoint string

	// 启用批处理
	Batching bool

	// 批处理最大导出数量
	BatchMaxExportSize int

	// 批处理延迟
	BatchDelay time.Duration

	// 启用传播器
	EnablePropagation bool

	// 启用调试信息
	Debug bool
}

// DefaultTracerConfig 返回默认追踪配置
func DefaultTracerConfig() TracerConfig {
	// 从环境变量获取服务名称，默认为"flow-service"
	serviceName := os.Getenv("OTEL_SERVICE_NAME")
	if serviceName == "" {
		serviceName = "flow-service"
	}

	// 从环境变量获取Jaeger端点，默认为""
	jaegerEndpoint := os.Getenv("JAEGER_ENDPOINT")

	// 从环境变量获取OTLP端点，默认为localhost:4317
	otlpEndpoint := os.Getenv("OTLP_ENDPOINT")
	if otlpEndpoint == "" {
		otlpEndpoint = "localhost:4317"
	}

	// 从环境变量获取采样率，默认为1.0
	samplingRate := 1.0
	if samplingRateStr := os.Getenv("OTEL_SAMPLING_RATE"); samplingRateStr != "" {
		if rate, err := fmt.Sscanf(samplingRateStr, "%f", &samplingRate); err != nil || rate != 1 {
			samplingRate = 1.0
		}
	}

	// 推断导出器类型
	exporterType := os.Getenv("OTEL_EXPORTER_TYPE")
	if exporterType == "" {
		// 自动推断
		if jaegerEndpoint != "" {
			exporterType = "jaeger"
		} else if otlpEndpoint != "" {
			exporterType = "otlp"
		} else {
			// 默认使用stdout
			exporterType = "stdout"
		}
	}

	return TracerConfig{
		ServiceName:        serviceName,
		ServiceVersion:     os.Getenv("SERVICE_VERSION"),
		Environment:        os.Getenv("SERVICE_ENV"),
		ExporterType:       exporterType,
		SamplingRate:       samplingRate,
		JaegerEndpoint:     jaegerEndpoint,
		OTLPEndpoint:       otlpEndpoint,
		Batching:           true,
		BatchMaxExportSize: 512,
		BatchDelay:         time.Second * 5,
		EnablePropagation:  true,
		Debug:              os.Getenv("OTEL_DEBUG") == "true",
	}
}

// TracingManager 分布式追踪管理器
type TracingManager struct {
	// 配置
	config TracerConfig

	// TracerProvider实例
	provider *sdktrace.TracerProvider

	// 默认Tracer实例
	tracer trace.Tracer

	// 关闭函数
	shutdown func(context.Context) error

	// 是否已初始化
	initialized bool
}

// NewTracingManager 创建分布式追踪管理器
func NewTracingManager(config TracerConfig) *TracingManager {
	if config.ServiceName == "" {
		config = DefaultTracerConfig()
	}

	return &TracingManager{
		config:      config,
		initialized: false,
	}
}

// Init 初始化分布式追踪
func (tm *TracingManager) Init() error {
	if tm.initialized {
		return nil
	}

	// 创建资源
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String(tm.config.ServiceName),
			semconv.ServiceVersionKey.String(tm.config.ServiceVersion),
			attribute.String("environment", tm.config.Environment),
		),
	)
	if err != nil {
		return fmt.Errorf("创建资源失败: %w", err)
	}

	// 创建导出器
	var exporter sdktrace.SpanExporter
	switch tm.config.ExporterType {
	case "stdout":
		var opts []stdouttrace.Option
		if tm.config.Debug {
			opts = append(opts, stdouttrace.WithPrettyPrint())
		}
		exporter, err = stdouttrace.New(opts...)
	case "jaeger":
		if tm.config.JaegerEndpoint == "" {
			return fmt.Errorf("Jaeger端点未配置")
		}
		exporter, err = jaeger.New(jaeger.WithCollectorEndpoint(jaeger.WithEndpoint(tm.config.JaegerEndpoint)))
	case "otlp":
		if tm.config.OTLPEndpoint == "" {
			return fmt.Errorf("OTLP端点未配置")
		}
		client := otlptracegrpc.NewClient(
			otlptracegrpc.WithEndpoint(tm.config.OTLPEndpoint),
			otlptracegrpc.WithInsecure(),
		)
		exporter, err = otlptrace.New(context.Background(), client)
	default:
		return fmt.Errorf("不支持的导出器类型: %s", tm.config.ExporterType)
	}

	if err != nil {
		return fmt.Errorf("创建导出器失败: %w", err)
	}

	// 创建批处理导出器
	var spanProcessor sdktrace.SpanProcessor
	if tm.config.Batching {
		batchOpts := []sdktrace.BatchSpanProcessorOption{
			sdktrace.WithMaxExportBatchSize(tm.config.BatchMaxExportSize),
			sdktrace.WithBatchTimeout(tm.config.BatchDelay),
		}
		spanProcessor = sdktrace.NewBatchSpanProcessor(exporter, batchOpts...)
	} else {
		spanProcessor = sdktrace.NewSimpleSpanProcessor(exporter)
	}

	// 配置采样器
	var sampler sdktrace.Sampler
	if tm.config.SamplingRate >= 1.0 {
		sampler = sdktrace.AlwaysSample()
	} else if tm.config.SamplingRate <= 0.0 {
		sampler = sdktrace.NeverSample()
	} else {
		sampler = sdktrace.TraceIDRatioBased(tm.config.SamplingRate)
	}

	// 创建TracerProvider
	opts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
		sdktrace.WithSpanProcessor(spanProcessor),
	}

	tm.provider = sdktrace.NewTracerProvider(opts...)
	tm.tracer = tm.provider.Tracer(tm.config.ServiceName)

	// 设置全局TracerProvider
	otel.SetTracerProvider(tm.provider)

	// 设置全局传播器
	if tm.config.EnablePropagation {
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		))
	}

	// 设置关闭函数
	tm.shutdown = func(ctx context.Context) error {
		// 关闭provider，确保所有span都被导出
		return tm.provider.Shutdown(ctx)
	}

	tm.initialized = true
	return nil
}

// Tracer 获取Tracer实例
func (tm *TracingManager) Tracer() trace.Tracer {
	if !tm.initialized {
		if err := tm.Init(); err != nil {
			// 如果初始化失败，返回NoopTracer
			return trace.NewNoopTracerProvider().Tracer("")
		}
	}
	return tm.tracer
}

// Provider 获取TracerProvider实例
func (tm *TracingManager) Provider() trace.TracerProvider {
	if !tm.initialized {
		if err := tm.Init(); err != nil {
			// 如果初始化失败，返回NoopTracerProvider
			return trace.NewNoopTracerProvider()
		}
	}
	return tm.provider
}

// Shutdown 关闭追踪管理器
func (tm *TracingManager) Shutdown(ctx context.Context) error {
	if !tm.initialized || tm.shutdown == nil {
		return nil
	}
	return tm.shutdown(ctx)
}

// 创建一个Tracer的便捷函数
func (tm *TracingManager) CreateTracer(name string) trace.Tracer {
	if !tm.initialized {
		if err := tm.Init(); err != nil {
			return trace.NewNoopTracerProvider().Tracer(name)
		}
	}
	return tm.provider.Tracer(name)
}

// TraceFunction 包装函数执行并添加追踪
func (tm *TracingManager) TraceFunction(ctx context.Context, name string, fn func(context.Context) error) error {
	ctx, span := tm.Tracer().Start(ctx, name)
	defer span.End()

	return fn(ctx)
}

// TraceHTTPMiddleware 创建HTTP追踪中间件
func (tm *TracingManager) TraceHTTPMiddleware() interface{} {
	// 这只是一个接口，实际的中间件实现将根据Web框架而有所不同
	// 在此处返回一个接口，应用程序需要为特定框架实现适当的中间件
	return nil
}

// OpenTelemetrySetup 创建一个全局使用的便捷函数
func OpenTelemetrySetup(serviceName string, options ...func(*TracerConfig)) (io.Closer, error) {
	config := DefaultTracerConfig()
	config.ServiceName = serviceName

	// 应用选项
	for _, option := range options {
		option(&config)
	}

	manager := NewTracingManager(config)
	if err := manager.Init(); err != nil {
		return nil, err
	}

	// 返回一个closer，用于在程序退出时关闭
	return &tracingCloser{manager: manager}, nil
}

// 用于支持io.Closer接口的类型
type tracingCloser struct {
	manager *TracingManager
}

// Close 实现io.Closer接口
func (tc *tracingCloser) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return tc.manager.Shutdown(ctx)
}
