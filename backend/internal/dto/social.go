package dto

type FollowRequest struct {
	BloggerID uint `json:"blogger_id" binding:"required"`
}

type UnfollowRequest struct {
	BloggerID uint `json:"blogger_id" binding:"required"`
}

type GetBloggersRequest struct {
	Uid      uint `form:"uid" binding:"required"`
	PageNum  uint `form:"page_num" binding:"required,min=1"`
	PageSize uint `form:"page_size" binding:"required,min=10,max=50"`
}

type GetFollowersRequest struct {
	Uid      uint `form:"uid" binding:"required"`
	PageNum  uint `form:"page_num" binding:"required,min=1"`
	PageSize uint `form:"page_size" binding:"required,min=10,max=50"`
}

type UserSocialSimpleResponse struct {
	Uid       uint   `json:"uid"`
	UserName  string `json:"user_name"`
	AvatarUrl string `json:"avatar_url"`
	IsFollow  bool   `json:"is_follow"`
}
