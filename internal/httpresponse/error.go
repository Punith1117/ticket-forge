package httpresponse

import "net/http"

type ErrorResponse struct {
	Error APIError `json:"error"`
}

type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func WriteError(
	w http.ResponseWriter,
	status int,
	code string,
	message string,
) error {
	return WriteJSON(w, status, ErrorResponse{
		Error: APIError{
			Code:    code,
			Message: message,
		},
	})
}

func WriteValidationError(
	w http.ResponseWriter,
	details []FieldError,
) error {
	return WriteJSON(w, http.StatusBadRequest, ErrorResponse{
		Error: APIError{
			Code:    "VALIDATION_ERROR",
			Message: "The request contains invalid fields.",
			Details: details,
		},
	})
}
