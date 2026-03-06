package providers

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

// AWSConfig AWS配置
type AWSConfig struct {
	// 区域
	Region string

	// 访问密钥ID
	AccessKeyID string

	// 访问密钥
	SecretAccessKey string

	// 会话令牌
	SessionToken string

	// 使用ECS容器凭证
	UseECSContainerCredential bool

	// 使用EC2实例配置文件
	UseEC2InstanceProfile bool

	// 使用SSO
	UseSSO bool

	// SSO配置文件
	SSOProfile string

	// 配置文件名
	ProfileName string

	// 配置文件路径
	ConfigurationPath string

	// 是否使用共享配置文件
	UseSharedConfig bool

	// 最大重试次数
	MaxRetries int

	// 端点URL（模拟本地使用）
	EndpointURL string

	// 连接超时（秒）
	ConnectionTimeout int
}

// DefaultAWSConfig 返回默认AWS配置
func DefaultAWSConfig() AWSConfig {
	return AWSConfig{
		Region:                    os.Getenv("AWS_REGION"),
		AccessKeyID:               os.Getenv("AWS_ACCESS_KEY_ID"),
		SecretAccessKey:           os.Getenv("AWS_SECRET_ACCESS_KEY"),
		SessionToken:              os.Getenv("AWS_SESSION_TOKEN"),
		UseECSContainerCredential: os.Getenv("AWS_CONTAINER_CREDENTIALS_RELATIVE_URI") != "",
		UseEC2InstanceProfile:     os.Getenv("AWS_EC2_METADATA_DISABLED") != "true",
		UseSSO:                    false,
		SSOProfile:                "",
		ProfileName:               os.Getenv("AWS_PROFILE"),
		UseSharedConfig:           true,
		MaxRetries:                3,
		ConnectionTimeout:         5,
	}
}

// AWSProvider AWS云服务提供商适配器
type AWSProvider struct {
	// 配置
	config AWSConfig

	// AWS配置
	awsConfig aws.Config

	// S3客户端
	s3Client *s3.Client

	// SQS客户端
	sqsClient *sqs.Client

	// SecretsManager客户端
	secretsClient *secretsmanager.Client

	// 已初始化
	initialized bool
}

// NewAWSProvider 创建AWS提供商适配器
func NewAWSProvider(cfg AWSConfig) (*AWSProvider, error) {
	if cfg.Region == "" {
		cfg = DefaultAWSConfig()
	}

	provider := &AWSProvider{
		config:      cfg,
		initialized: false,
	}

	return provider, nil
}

// Init 初始化AWS提供商
func (p *AWSProvider) Init(ctx context.Context) error {
	if p.initialized {
		return nil
	}

	// 创建AWS配置选项
	optFns := []func(*config.LoadOptions) error{
		config.WithRegion(p.config.Region),
		config.WithRetryMaxAttempts(p.config.MaxRetries),
		config.WithClientLogMode(aws.LogRetries | aws.LogRequest),
	}

	// 设置凭证提供者
	if p.config.AccessKeyID != "" && p.config.SecretAccessKey != "" {
		optFns = append(optFns, config.WithCredentialsProvider(
			aws.CredentialsProviderFunc(func(ctx context.Context) (aws.Credentials, error) {
				return aws.Credentials{
					AccessKeyID:     p.config.AccessKeyID,
					SecretAccessKey: p.config.SecretAccessKey,
					SessionToken:    p.config.SessionToken,
					Source:          "Flow AWS Provider",
				}, nil
			}),
		))
	}

	// 使用共享配置
	if p.config.UseSharedConfig {
		optFns = append(optFns, config.WithSharedConfigProfile(p.config.ProfileName))
	}

	// 自定义端点（用于本地开发/测试）
	if p.config.EndpointURL != "" {
		customResolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
			return aws.Endpoint{
				URL:           p.config.EndpointURL,
				SigningRegion: region,
			}, nil
		})
		optFns = append(optFns, config.WithEndpointResolverWithOptions(customResolver))
	}

	// 加载AWS配置
	cfg, err := config.LoadDefaultConfig(ctx, optFns...)
	if err != nil {
		return fmt.Errorf("加载AWS配置失败: %w", err)
	}

	p.awsConfig = cfg

	// 创建S3客户端
	p.s3Client = s3.NewFromConfig(cfg)

	// 创建SQS客户端
	p.sqsClient = sqs.NewFromConfig(cfg)

	// 创建SecretsManager客户端
	p.secretsClient = secretsmanager.NewFromConfig(cfg)

	p.initialized = true
	return nil
}

// Provider 返回提供商名称
func (p *AWSProvider) Provider() string {
	return "aws"
}

// S3 获取S3客户端
func (p *AWSProvider) S3() (*s3.Client, error) {
	if !p.initialized {
		if err := p.Init(context.Background()); err != nil {
			return nil, err
		}
	}
	return p.s3Client, nil
}

// SQS 获取SQS客户端
func (p *AWSProvider) SQS() (*sqs.Client, error) {
	if !p.initialized {
		if err := p.Init(context.Background()); err != nil {
			return nil, err
		}
	}
	return p.sqsClient, nil
}

// SecretsManager 获取SecretsManager客户端
func (p *AWSProvider) SecretsManager() (*secretsmanager.Client, error) {
	if !p.initialized {
		if err := p.Init(context.Background()); err != nil {
			return nil, err
		}
	}
	return p.secretsClient, nil
}

// IsRunningInECS 检查是否在ECS环境中运行
func (p *AWSProvider) IsRunningInECS() bool {
	return os.Getenv("AWS_CONTAINER_CREDENTIALS_RELATIVE_URI") != "" ||
		os.Getenv("ECS_CONTAINER_METADATA_URI") != ""
}

// IsRunningInEC2 检查是否在EC2环境中运行
func (p *AWSProvider) IsRunningInEC2() bool {
	// 一种简单的方法是检查EC2特有的元数据文件
	if _, err := os.Stat("/sys/devices/virtual/dmi/id/product_uuid"); err == nil {
		return true
	}

	// 检查/sys/hypervisor/uuid，在EC2上通常存在
	if data, err := os.ReadFile("/sys/hypervisor/uuid"); err == nil {
		return strings.HasPrefix(string(data), "ec2")
	}

	return false
}

// GetSecret 从SecretsManager获取密钥
func (p *AWSProvider) GetSecret(ctx context.Context, secretName string) (string, error) {
	client, err := p.SecretsManager()
	if err != nil {
		return "", err
	}

	input := &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(secretName),
	}

	result, err := client.GetSecretValue(ctx, input)
	if err != nil {
		return "", fmt.Errorf("获取密钥失败: %w", err)
	}

	// 返回密钥内容
	if result.SecretString != nil {
		return *result.SecretString, nil
	}

	return "", fmt.Errorf("密钥为空")
}

// UploadToS3 上传文件到S3
func (p *AWSProvider) UploadToS3(ctx context.Context, bucket, key string, data []byte) error {
	client, err := p.S3()
	if err != nil {
		return err
	}

	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   strings.NewReader(string(data)),
	})

	if err != nil {
		return fmt.Errorf("上传到S3失败: %w", err)
	}

	return nil
}

// SendSQSMessage 发送消息到SQS
func (p *AWSProvider) SendSQSMessage(ctx context.Context, queueURL, message string) error {
	client, err := p.SQS()
	if err != nil {
		return err
	}

	_, err = client.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    aws.String(queueURL),
		MessageBody: aws.String(message),
	})

	if err != nil {
		return fmt.Errorf("发送SQS消息失败: %w", err)
	}

	return nil
}

// DetectRegion 自动检测当前区域
func (p *AWSProvider) DetectRegion() string {
	// 首先从环境变量检查
	if region := os.Getenv("AWS_REGION"); region != "" {
		return region
	}

	if region := os.Getenv("AWS_DEFAULT_REGION"); region != "" {
		return region
	}

	// 尝试从EC2元数据服务获取区域
	// 实际实现会更复杂，这里简化处理

	// 返回默认区域
	return "us-east-1"
}

// CheckConnectionHealth 检查AWS连接健康状态
func (p *AWSProvider) CheckConnectionHealth(ctx context.Context) error {
	// 设置超时上下文
	ctx, cancel := context.WithTimeout(ctx, time.Duration(p.config.ConnectionTimeout)*time.Second)
	defer cancel()

	// 简单测试S3连接
	client, err := p.S3()
	if err != nil {
		return err
	}

	_, err = client.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		return fmt.Errorf("AWS连接检查失败: %w", err)
	}

	return nil
}
