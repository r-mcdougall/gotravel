package booking

import "context"

type Service interface {
	Hold(ctx context.Context, userID string, request HoldRequest) (Response, error)
	GetBooking(ctx context.Context, userID, id string) (Response, error)
	ListBookings(ctx context.Context, userID string, request ListRequest) (ListResponse, error)
	CancelBooking(ctx context.Context, userID, id string) (Response, error)
}
