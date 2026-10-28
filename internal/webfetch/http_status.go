package webfetch

import "net/http"

// responseStatusError is shared by static and rendered main-document fetches.
func responseStatusError(rawURL string, statusCode int, status, body string) error {
	if statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices {
		return nil
	}
	httpErr := &HTTPError{
		URL:        rawURL,
		StatusCode: statusCode,
		Status:     status,
		Body:       truncate(body, maxErrorBody),
	}
	return newCodedError(httpErr, httpErrorCode(statusCode), httpErrorSuggestion(statusCode))
}
