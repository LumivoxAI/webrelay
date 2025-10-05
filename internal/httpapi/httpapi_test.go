package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"
)

type HTTPAPISuite struct {
	suite.Suite
}

func (s *HTTPAPISuite) TestUsesSafeClientRequestIDInErrorResponse() {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/unknown", nil)
	request.Header.Set("X-Request-ID", "client-request_42")

	NewHandler(zap.NewNop()).ServeHTTP(response, request)

	s.Equal(http.StatusNotFound, response.Code)
	s.Equal("client-request_42", response.Header().Get("X-Request-ID"))
	var body errorResponse
	s.Require().NoError(json.Unmarshal(response.Body.Bytes(), &body))
	s.Equal(CODE_DOCUMENT_NOT_FOUND, body.Error.Code)
	s.Equal("client-request_42", body.Error.RequestID)
}

func (s *HTTPAPISuite) TestReplacesUnsafeClientRequestID() {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/unknown", nil)
	request.Header.Set("X-Request-ID", "unsafe request id")

	NewHandler(zap.NewNop()).ServeHTTP(response, request)

	requestID := response.Header().Get("X-Request-ID")
	s.NotEmpty(requestID)
	s.NotEqual("unsafe request id", requestID)
	s.Regexp(`^[A-Z0-9]{26}$`, requestID)
}

func (s *HTTPAPISuite) TestDecodeJSONRejectsInvalidBodies() {
	testCases := []struct {
		name        string
		contentType string
		body        string
		message     string
	}{
		{name: "missing content type", body: `{}`, message: "Content-Type must be application/json"},
		{name: "unknown field", contentType: "application/json", body: `{"unknown":true}`, message: "request contains an unknown field"},
		{name: "multiple values", contentType: "application/json", body: `{} {}`, message: "request body must contain exactly one JSON object"},
		{name: "empty body", contentType: "application/json", body: "", message: "request body is required"},
	}

	for _, testCase := range testCases {
		s.Run(testCase.name, func() {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(testCase.body))
			if testCase.contentType != "" {
				request.Header.Set("Content-Type", testCase.contentType)
			}

			var target FetchRequest
			apiError := DecodeJSON(response, request, &target, 1024)
			s.Require().NotNil(apiError)
			s.Equal(CODE_INVALID_REQUEST, apiError.Code)
			s.Equal(testCase.message, apiError.Message)
		})
	}
}

func (s *HTTPAPISuite) TestDecodeJSONRejectsOversizedBody() {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"url":"https://example.com"}`))
	request.Header.Set("Content-Type", "application/json")

	var target FetchRequest
	apiError := DecodeJSON(response, request, &target, 8)

	s.Require().NotNil(apiError)
	s.Equal(CODE_INVALID_REQUEST, apiError.Code)
	s.Equal("request body is too large", apiError.Message)
}

func (s *HTTPAPISuite) TestErrorCodeHTTPStatuses() {
	testCases := map[Code]int{
		CODE_INVALID_REQUEST:       http.StatusBadRequest,
		CODE_DOCUMENT_NOT_FOUND:    http.StatusNotFound,
		CODE_RANGE_NOT_SATISFIABLE: http.StatusRequestedRangeNotSatisfiable,
		CODE_UNSUPPORTED_URL:       http.StatusUnprocessableEntity,
		CODE_ALL_PROVIDERS_FAILED:  http.StatusBadGateway,
		CODE_UPSTREAM_TIMEOUT:      http.StatusGatewayTimeout,
		CODE_INTERNAL:              http.StatusInternalServerError,
	}

	for code, expectedStatus := range testCases {
		s.Equal(expectedStatus, code.HTTPStatus(), string(code))
	}
}

func TestHTTPAPISuite(t *testing.T) {
	suite.Run(t, new(HTTPAPISuite))
}
