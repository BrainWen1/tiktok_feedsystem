package dto

// FeedListReq 通用游标分页入参，5个list接口复用
type FeedListReq struct {
	LastVid string `form:"last_vid"` // 游标，""代表第一页
	Limit   int    `form:"limit"`
	Tag     string `form:"tag"` // listByTag 专用，其他接口忽略
}

// FeedItemResp feed对外输出VO，聚合完成后的视频条目
type FeedItemResp struct {
	Vid        string `json:"vid"`
	Title      string `json:"title"`
	Desc       string `json:"desc"`
	VideoUrl   string `json:"video_url"`
	CoverUrl   string `json:"cover_url"`
	AuthorUid  uint   `json:"author_uid"`
	AuthorName string `json:"author_name"`
	AvatarUrl  string `json:"avatar_url"`

	LikeCnt    int64 `json:"like_cnt"`    // 点赞总数
	CommentCnt int64 `json:"comment_cnt"` // 评论总数

	IsLike   bool `json:"is_like"`   // 当前登录用户是否点赞该视频
	IsFollow bool `json:"is_follow"` // 当前登录用户是否关注作者
}
