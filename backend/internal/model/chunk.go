package model

import "time"

const (
	// ChunkSize 默认分片大小 5MB
	ChunkSize int64 = 5 << 20
	// MaxChunkUploadSize 单个分片上传任务的成品文件上限 2GB
	MaxChunkUploadSize int64 = 2 << 30
	// MaxChunkCount 由文件上限与分片大小推导，防止异常请求构造超大分片数组
	MaxChunkCount = int(MaxChunkUploadSize/ChunkSize) + 1
	// ChunkSessionTTL 上传会话有效期。每次成功上传分片都会滑动续期，
	// 与定时 GC 的 24 小时过期阈值保持一致。
	ChunkSessionTTL = 24 * time.Hour
)

// ChunkUploadSession 分片上传会话，元数据存在 Redis
type ChunkUploadSession struct {
	UploadID     string    `json:"upload_id"`     // 上传唯一标识，uuid
	AccountID    uint      `json:"account_id"`    // 上传用户id
	Filename     string    `json:"filename"`      // 原始文件名（仅用于展示与后缀校验）
	FileSize     int64     `json:"file_size"`     // 文件总大小 byte
	ChunkSize    int64     `json:"chunk_size"`    // 单片分片大小
	TotalChunks  int       `json:"total_chunks"`  // 分片总数
	FileHash     string    `json:"file_hash"`     // 整个文件sha256，用于校验；为空表示不校验
	UploadedBits []bool    `json:"uploaded_bits"` // 分片标记位，true=已上传
	ExpireAt     time.Time `json:"expire_at"`     // 会话过期时间
}

// UploadedChunks 返回已经上传成功的分片下标列表
func (s *ChunkUploadSession) UploadedChunks() []int {
	var indices []int
	for idx, ok := range s.UploadedBits {
		if ok {
			indices = append(indices, idx)
		}
	}
	return indices
}

// IsAllDone 判断是否全部分片上传完成。
// 必须同时校验分片总数：空标记位或长度不一致都视为未完成，避免空会话被误判为可合并。
func (s *ChunkUploadSession) IsAllDone() bool {
	if s.TotalChunks <= 0 || len(s.UploadedBits) != s.TotalChunks {
		return false
	}
	for _, ok := range s.UploadedBits {
		if !ok {
			return false
		}
	}
	return true
}

// ExpectedChunkSize 返回指定分片应包含的字节数：
// 除最后一片外均为 ChunkSize，最后一片为剩余大小。
func (s *ChunkUploadSession) ExpectedChunkSize(index int) int64 {
	if index < s.TotalChunks-1 {
		return s.ChunkSize
	}
	return s.FileSize - int64(s.TotalChunks-1)*s.ChunkSize
}
