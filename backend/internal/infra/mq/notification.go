package mq

type NotificationEvent struct {
	EventID  string
	Action   string // like, comment, follow
	ActorID  uint
	TargetID uint // VideoID, CommentID 等
}
