package dto

// 注册用户请求
type RegisterRequest struct {
	Username string `json:"user_name"`
	Password string `json:"password"`
}

// 重命名请求
type RenameRequest struct {
	NewUsername string `json:"new_user_name"`
}

// FindByIDResponse 查询用户信息响应
type FindByIDResponse struct {
	ID        uint   `json:"id"`
	Username  string `json:"user_name"`
	AvatarURL string `json:"avatar_url,omitempty"`
	Bio       string `json:"bio,omitempty"`
}

// FindByUsernameResponse
type FindByUsernameResponse struct {
	ID       uint   `json:"id"`
	Username string `json:"user_name"`
}

// 修改密码请求
type ChangePasswordRequest struct {
	Username    string `json:"user_name"`
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

// 登陆请求
type LoginRequest struct {
	Username string `json:"user_name"`
	Password string `json:"password"`
}

// 登陆响应
type LoginResponse struct {
	Token        string `json:"token"`
	RefreshToken string `json:"refresh_token"`
	Username     string `json:"user_name"`
}

// 更新用户资料请求
type UpdateProfileRequest struct {
	AvatarURL string `json:"avatar_url"`
	Bio       string `json:"bio"`
}

// 刷新Token请求
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// GetProfileResponse 用户主页资料响应，统计字段，业务中redis/异步计数，禁止实时count大表
type GetProfileResponse struct {
	Account       FindByIDResponse `json:"account"`
	VideoCount    int64            `json:"video_count"`
	TotalLikes    int64            `json:"total_likes"`
	FollowerCount int64            `json:"follower_count"`
	VloggerCount  int64            `json:"vlogger_count"`
}
