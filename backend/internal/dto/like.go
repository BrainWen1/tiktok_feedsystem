package dto

type LikeRequest struct {
	VideoID uint `json:"video_id" binding:"required"`
}

type UnlikeRequest struct {
	VideoID uint `json:"video_id" binding:"required"`
}

type IsLikedRequest struct {
	VideoID uint `json:"video_id" binding:"required"`
}

type IsLikedResponse struct {
	IsLiked bool `json:"is_liked"`
}
