package auth

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegisterRequestValidate_acceptsValidRequests(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*RegisterRequest)
	}{
		{"required fields only", func(*RegisterRequest) {}},
		{"international phone", func(r *RegisterRequest) { r.Phone = ptr("+6281234567890") }},
		{"local phone", func(r *RegisterRequest) { r.Phone = ptr("08123456789") }},
		{"password of 8 bytes", func(r *RegisterRequest) { r.Password = strings.Repeat("a", 8) }},
		{"password of 72 bytes", func(r *RegisterRequest) { r.Password = strings.Repeat("a", 72) }},
		{"full name of 100 multi-byte characters", func(r *RegisterRequest) { r.FullName = strings.Repeat("é", 100) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := validRegister()
			tt.mutate(&request)

			require.NoError(t, request.Validate())
		})
	}
}

func TestRegisterRequestValidate_reportsInvalidFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*RegisterRequest)
		want   []string
	}{
		{"empty email", func(r *RegisterRequest) { r.Email = "" }, []string{"email"}},
		{"email without domain", func(r *RegisterRequest) { r.Email = "john.doe@" }, []string{"email"}},
		{"email without at sign", func(r *RegisterRequest) { r.Email = "john.doe.example.com" }, []string{"email"}},
		{"email with display name", func(r *RegisterRequest) { r.Email = "John Doe <john.doe@example.com>" }, []string{"email"}},
		{"email over 254 characters", func(r *RegisterRequest) { r.Email = strings.Repeat("a", 250) + "@example.com" }, []string{"email"}},
		{"empty password", func(r *RegisterRequest) { r.Password = "" }, []string{"password"}},
		{"password of 7 bytes", func(r *RegisterRequest) { r.Password = strings.Repeat("a", 7) }, []string{"password"}},
		{"password of 73 bytes", func(r *RegisterRequest) { r.Password = strings.Repeat("a", 73) }, []string{"password"}},
		{"password counted in bytes not characters", func(r *RegisterRequest) { r.Password = strings.Repeat("é", 37) }, []string{"password"}},
		{"empty full name", func(r *RegisterRequest) { r.FullName = "" }, []string{"full_name"}},
		{"full name of 101 characters", func(r *RegisterRequest) { r.FullName = strings.Repeat("a", 101) }, []string{"full_name"}},
		{"phone with letters", func(r *RegisterRequest) { r.Phone = ptr("08123abc") }, []string{"phone"}},
		{"phone too short", func(r *RegisterRequest) { r.Phone = ptr("0812") }, []string{"phone"}},
		{"phone too long", func(r *RegisterRequest) { r.Phone = ptr("1234567890123456") }, []string{"phone"}},
		{"phone with spaces", func(r *RegisterRequest) { r.Phone = ptr("+62 812 3456 7890") }, []string{"phone"}},
		{"phone with two plus signs", func(r *RegisterRequest) { r.Phone = ptr("++6281234567890") }, []string{"phone"}},
		{"empty phone", func(r *RegisterRequest) { r.Phone = ptr("") }, []string{"phone"}},
		{"nothing set", func(r *RegisterRequest) { *r = RegisterRequest{} }, []string{"email", "password", "full_name"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := validRegister()
			tt.mutate(&request)

			err := request.Validate()

			require.ElementsMatch(t, tt.want, validationFields(t, err))
		})
	}
}

func TestLoginRequestValidate(t *testing.T) {
	tests := []struct {
		name    string
		request LoginRequest
		want    []string
	}{
		{"both set", LoginRequest{Email: "john.doe@example.com", Password: "x"}, nil},
		{"missing email", LoginRequest{Password: "x"}, []string{"email"}},
		{"missing password", LoginRequest{Email: "john.doe@example.com"}, []string{"password"}},
		{"nothing set", LoginRequest{}, []string{"email", "password"}},
		{"email of 254 characters", LoginRequest{Email: strings.Repeat("a", 242) + "@example.com", Password: "x"}, nil},
		{"email over 254 characters", LoginRequest{Email: strings.Repeat("a", 243) + "@example.com", Password: "x"}, []string{"email"}},
		{"password of 72 bytes", LoginRequest{Email: "john.doe@example.com", Password: strings.Repeat("a", 72)}, nil},
		{"password of 73 bytes", LoginRequest{Email: "john.doe@example.com", Password: strings.Repeat("a", 73)}, []string{"password"}},
		{"password counted in bytes not characters", LoginRequest{Email: "john.doe@example.com", Password: strings.Repeat("é", 37)}, []string{"password"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.request.Validate()

			if tt.want == nil {
				require.NoError(t, err)
				return
			}
			require.ElementsMatch(t, tt.want, validationFields(t, err))
		})
	}
}

func TestUpdateProfileRequestValidate(t *testing.T) {
	tests := []struct {
		name    string
		request UpdateProfileRequest
		want    []string
	}{
		{"empty request", UpdateProfileRequest{}, nil},
		{"full name only", UpdateProfileRequest{FullName: ptr("Jane Smith")}, nil},
		{"phone value", UpdateProfileRequest{Phone: NullableString{Set: true, Value: ptr("08123456789")}}, nil},
		{"phone cleared", UpdateProfileRequest{Phone: NullableString{Set: true}}, nil},
		{"empty full name", UpdateProfileRequest{FullName: ptr("")}, []string{"full_name"}},
		{"full name of 101 characters", UpdateProfileRequest{FullName: ptr(strings.Repeat("a", 101))}, []string{"full_name"}},
		{"invalid phone", UpdateProfileRequest{Phone: NullableString{Set: true, Value: ptr("12345")}}, []string{"phone"}},
		{"both invalid", UpdateProfileRequest{FullName: ptr(""), Phone: NullableString{Set: true, Value: ptr("x")}}, []string{"full_name", "phone"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.request.Validate()

			if tt.want == nil {
				require.NoError(t, err)
				return
			}
			require.ElementsMatch(t, tt.want, validationFields(t, err))
		})
	}
}

func TestUpdateProfileRequest_distinguishesOmittedNullAndValue(t *testing.T) {
	tests := []struct {
		name string
		body string
		want UpdateProfileRequest
	}{
		{"nothing sent", `{}`, UpdateProfileRequest{}},
		{"phone omitted", `{"full_name":"Jane Smith"}`, UpdateProfileRequest{FullName: ptr("Jane Smith")}},
		{"phone null", `{"phone":null}`, UpdateProfileRequest{Phone: NullableString{Set: true}}},
		{"phone value", `{"phone":"08123456789"}`, UpdateProfileRequest{Phone: NullableString{Set: true, Value: ptr("08123456789")}}},
		{"empty phone is a value", `{"phone":""}`, UpdateProfileRequest{Phone: NullableString{Set: true, Value: ptr("")}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got UpdateProfileRequest

			require.NoError(t, json.Unmarshal([]byte(tt.body), &got))

			require.Equal(t, tt.want, got)
		})
	}
}

func TestUpdateProfileRequest_rejectsNonStringPhone(t *testing.T) {
	var got UpdateProfileRequest

	err := json.Unmarshal([]byte(`{"phone":123}`), &got)

	require.Error(t, err)
}
