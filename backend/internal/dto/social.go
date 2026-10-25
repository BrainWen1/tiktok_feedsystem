package dto

import "time"

type FollowRequest struct {
	BloggerID uint `json:"blogger_id" binding:"required"`
}

type UnfollowRequest struct {
	BloggerID uint `json:"blogger_id" binding:"required"`
}

// GetBloggersRequest 关注列表请求。
// page_num 仅为兼容旧客户端保留，实际分页使用 cursor.last_uid + cursor.last_created_at。
type GetBloggersRequest struct {
	Uid                 uint      `form:"uid" binding:"required"`
	PageNum             uint      `form:"page_num" binding:"omitempty,min=1"`
	PageSize            uint      `form:"page_size" binding:"required,min=10,max=50"`
	CursorLastUid       string    `form:"cursor.last_uid"`
	CursorLastCreatedAt time.Time `form:"cursor.last_created_at"`
}

// GetFollowersRequest 粉丝列表请求，游标字段语义同 GetBloggersRequest。
type GetFollowersRequest struct {
	Uid                 uint      `form:"uid" binding:"required"`
	PageNum             uint      `form:"page_num" binding:"omitempty,min=1"`
	PageSize            uint      `form:"page_size" binding:"required,min=10,max=50"`
	CursorLastUid       string    `form:"cursor.last_uid"`
	CursorLastCreatedAt time.Time `form:"cursor.last_created_at"`
}

type UserSocialSimpleResponse struct {
	Uid       uint   `json:"uid"`
	UserName  string `json:"user_name"`
	AvatarUrl string `json:"avatar_url"`
	IsFollow  bool   `json:"is_follow"`
}

type IsFollowRequest struct {
	TargetID uint `form:"target_id" binding:"required"`
}

type IsFollowResponse struct {
	IsFollow bool `json:"is_follow"`
	IsFan    bool `json:"is_fan"`
}

type GetCountsRequest struct {
	TargetID uint `form:"target_id" binding:"required"`
}
