package dto

type FollowRequest struct {
	BloggerID uint `json:"blogger_id" binding:"required"`
}

type UnfollowRequest struct {
	BloggerID uint `json:"blogger_id" binding:"required"`
}
