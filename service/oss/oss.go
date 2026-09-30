// Package oss 提供审计正文用的 S3 兼容对象存储抽象。
// 一套 S3 协议实现同时适配 AWS S3、阿里云 OSS(S3 网关)、腾讯 COS、自建 MinIO，
// 通过 AUDIT_OSS_ENDPOINT / AUDIT_OSS_FORCE_PATH_STYLE 区分。
package oss

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"

	"github.com/QuantumNous/new-api/common"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Uploader 审计正文对象存储接口。
type Uploader interface {
	Put(ctx context.Context, key string, data []byte, contentType string) error
	PutReader(ctx context.Context, key string, reader io.Reader, contentType string) error
	Get(ctx context.Context, key string) ([]byte, error)
	Head(ctx context.Context, key string) (*ObjectInfo, error)
	Delete(ctx context.Context, key string) error
}

type ObjectInfo struct {
	SizeBytes int64
}

var (
	defaultUploader Uploader
	uploaderOnce    sync.Once
	uploaderErr     error
)

// GetUploader 返回基于当前配置的单例 Uploader；OSS 未配置时返回错误。
func GetUploader() (Uploader, error) {
	uploaderOnce.Do(func() {
		defaultUploader, uploaderErr = newS3Uploader()
	})
	return defaultUploader, uploaderErr
}

// s3Uploader 基于 aws-sdk-go-v2 的 S3 兼容实现。
type s3Uploader struct {
	client *s3.Client
	bucket string
}

func newS3Uploader() (Uploader, error) {
	if !common.AuditLogOSSConfigured() {
		return nil, errors.New("audit OSS not configured (AUDIT_OSS_BUCKET empty)")
	}

	cred := credentials.NewStaticCredentialsProvider(
		common.AuditOSSAccessKey, common.AuditOSSSecretKey, "")

	region := common.AuditOSSRegion
	if region == "" {
		region = "us-east-1" // 兼容 MinIO 等无 region 概念的存储
	}

	opts := s3.Options{
		Region:       region,
		Credentials:  cred,
		UsePathStyle: common.AuditOSSForcePathStyle,
	}
	if common.AuditOSSEndpoint != "" {
		opts.BaseEndpoint = aws.String(common.AuditOSSEndpoint)
	}

	client := s3.New(opts)
	return &s3Uploader{client: client, bucket: common.AuditOSSBucket}, nil
}

func (u *s3Uploader) Put(ctx context.Context, key string, data []byte, contentType string) error {
	return u.PutReader(ctx, key, bytes.NewReader(data), contentType)
}

func (u *s3Uploader) PutReader(ctx context.Context, key string, reader io.Reader, contentType string) error {
	ct := contentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	_, err := u.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(u.bucket),
		Key:         aws.String(key),
		Body:        reader,
		ContentType: aws.String(ct),
	})
	return err
}

func (u *s3Uploader) Get(ctx context.Context, key string) ([]byte, error) {
	out, err := u.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(u.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, err
	}
	defer out.Body.Close()
	var buf bytes.Buffer
	if _, err = buf.ReadFrom(out.Body); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (u *s3Uploader) Head(ctx context.Context, key string) (*ObjectInfo, error) {
	out, err := u.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(u.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, err
	}
	size := int64(0)
	if out.ContentLength != nil {
		size = *out.ContentLength
	}
	return &ObjectInfo{SizeBytes: size}, nil
}

func (u *s3Uploader) Delete(ctx context.Context, key string) error {
	_, err := u.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(u.bucket),
		Key:    aws.String(key),
	})
	return err
}
