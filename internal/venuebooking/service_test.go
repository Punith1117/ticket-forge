package venuebooking

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestValidateSchedule(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)

	service := &Service{
		now: func() time.Time {
			return now
		},
	}

	tests := []struct {
		name    string
		request CreateVenueBookingRequest
		wantErr error
	}{
		{
			name: "valid schedule",
			request: CreateVenueBookingRequest{
				StartsAt:        now.Add(time.Hour),
				DurationMinutes: 60,
			},
		},
		{
			name: "zero start time",
			request: CreateVenueBookingRequest{
				DurationMinutes: 60,
			},
			wantErr: ErrInvalidSchedule,
		},
		{
			name: "duration below minimum",
			request: CreateVenueBookingRequest{
				StartsAt:        now.Add(time.Hour),
				DurationMinutes: 14,
			},
			wantErr: ErrInvalidDuration,
		},
		{
			name: "duration at minimum boundary",
			request: CreateVenueBookingRequest{
				StartsAt:        now.Add(time.Hour),
				DurationMinutes: 15,
			},
		},
		{
			name: "duration at maximum boundary",
			request: CreateVenueBookingRequest{
				StartsAt:        now.Add(time.Hour),
				DurationMinutes: 7 * 24 * 60,
			},
		},
		{
			name: "duration above maximum",
			request: CreateVenueBookingRequest{
				StartsAt:        now.Add(time.Hour),
				DurationMinutes: 7*24*60 + 1,
			},
			wantErr: ErrInvalidDuration,
		},
		{
			name: "start time below minimum notice",
			request: CreateVenueBookingRequest{
				StartsAt:        now.Add(14*time.Minute + 59*time.Second),
				DurationMinutes: 60,
			},
			wantErr: ErrStartTimeTooSoon,
		},
		{
			name: "start time at minimum notice boundary",
			request: CreateVenueBookingRequest{
				StartsAt:        now.Add(15 * time.Minute),
				DurationMinutes: 60,
			},
		},
		{
			name: "start time in the past",
			request: CreateVenueBookingRequest{
				StartsAt:        now.Add(-time.Minute),
				DurationMinutes: 60,
			},
			wantErr: ErrStartTimeTooSoon,
		},
		{
			name: "start time beyond one calendar year",
			request: CreateVenueBookingRequest{
				StartsAt:        now.AddDate(1, 0, 0).Add(time.Nanosecond),
				DurationMinutes: 60,
			},
			wantErr: ErrStartTimeTooFar,
		},
		{
			name: "start time at one calendar year boundary",
			request: CreateVenueBookingRequest{
				StartsAt:        now.AddDate(1, 0, 0),
				DurationMinutes: 60,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := service.validateSchedule(tt.request)

			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}

			if err != tt.wantErr {
				t.Fatalf("expected error %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestParseUUID(t *testing.T) {
	validID := uuid.New()

	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:  "valid UUID",
			input: validID.String(),
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:    "invalid UUID",
			input:   "not-a-uuid",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseUUID(tt.input)

			if (err != nil) != tt.wantErr {
				t.Fatalf("expected error=%v, got error=%v", tt.wantErr, err)
			}

			if err != nil {
				return
			}

			if !got.Valid {
				t.Fatal("expected UUID to be valid")
			}

			expected, err := uuid.Parse(tt.input)
			if err != nil {
				t.Fatal(err)
			}

			if got.String() != expected.String() {
				t.Fatalf("expected UUID %s, got %s", expected, got.String())
			}
		})
	}
}
