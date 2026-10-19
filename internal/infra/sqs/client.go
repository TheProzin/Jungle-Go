package sqs

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	sqsservice "github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/TheProzin/Jungle-Go/internal/config"
)

func NewClient(cfg *config.Config) (*sqsservice.Client, error) {
	options := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(cfg.AWSRegion),
	}
	if cfg.AWSAccessKeyID != "" {
		options = append(options, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AWSAccessKeyID, cfg.AWSSecretAccessKey, ""),
		))
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(), options...)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}

	return sqsservice.NewFromConfig(awsCfg, func(options *sqsservice.Options) {
		if cfg.AWSEndpoint != "" {
			options.BaseEndpoint = aws.String(cfg.AWSEndpoint)
		}
	}), nil
}
