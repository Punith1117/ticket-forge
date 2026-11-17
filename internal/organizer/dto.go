package organizer

import "time"

type ProfileResponse struct {
	ID          string    `json:"id"`
	DisplayName string    `json:"display_name"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

type CreateProfileResponse struct {
	Message string          `json:"message"`
	Profile ProfileResponse `json:"profile"`
}
