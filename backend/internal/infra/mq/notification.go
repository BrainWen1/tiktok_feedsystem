package mq

type NotificationEvent struct {
	EventID  string
	Action   string // like, comment, follow
	ActorID  uint
	UserName string

	VideoID   uint   // 视频ID
	CommentID uint   // 评论ID
	ParentID  uint   // 父评论ID，顶级评论为0，用来区分通知视频作者还是父评论作者
	Content   string // 评论内容，用于通知消息中显示
}
