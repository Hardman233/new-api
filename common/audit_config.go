package common

// 审计留存（请求/响应正文）相关配置。
// 设计见 New-API二开-请求响应审计留存方案.md 与 OSS 落库补传方案。
// 分流原则：纯文本正文全量入库；二进制正文走本地落盘 + OSS，库里只记路径。
var (
	// 总开关，默认关闭
	AuditLogEnabled bool
	// 是否保存请求体
	AuditLogCaptureRequest bool
	// 是否保存响应体
	AuditLogCaptureResponse bool
	// 单个请求体/响应体最多保存字节数（纯文本截断阈值）
	AuditLogMaxBodyBytes int
	// 留存天数
	AuditLogRetentionDays int
	// 异步队列长度
	AuditLogQueueSize int
	// 抽样率，1.0 全量
	AuditLogSampleRate float64
	// 只审计指定分组，逗号分隔，空表示全部
	AuditLogGroupAllowlist string
	// 只审计指定模型，逗号分隔，空表示全部
	AuditLogModelAllowlist string
	// 审计正文加密密钥（AES-GCM），生产必须手动生成
	AuditLogSecret string
	// 是否记录客户端 IP
	AuditLogRecordIp bool

	// 本地 spool 目录容量上限（MB），超限优先清理已上传副本
	AuditLogSpoolMaxMB int

	// === OSS（S3 兼容）配置，仅二进制正文使用 ===
	AuditOSSEndpoint       string
	AuditOSSRegion         string
	AuditOSSBucket         string
	AuditOSSAccessKey      string
	AuditOSSSecretKey      string
	AuditOSSKeyPrefix      string
	AuditOSSForcePathStyle bool

	// 上传 worker 数
	AuditUploadWorkers int
	// 单条二进制上传最大重试次数，超过标记 abandoned
	AuditUploadMaxRetry int
	// master 闲时补传/清理任务间隔（秒）
	AuditReconcileIntervalSec       int
	UpstreamResponseLogEnabled      bool
	UpstreamResponseLogMaxBodyBytes int
	UpstreamResponseLogMediaToOSS   bool
)

// AuditLogOSSConfigured 判断 OSS 是否已配置（bucket 非空即视为启用 OSS 落库）。
func AuditLogOSSConfigured() bool {
	return AuditOSSBucket != ""
}
