package webfetch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

var supportedImageTypes = map[string]bool{
	"image/avif": true,
	"image/gif":  true,
	"image/jpeg": true,
	"image/png":  true,
	"image/webp": true,
}

// fetchImageByContentType sniffs the target's Content-Type via HEAD and returns
// an image document when supported. It is the extensionless fallback used by
// raw fetches, where an extra HEAD is acceptable because the caller asked for
// unprocessed content.
func (s *Service) fetchImageByContentType(ctx context.Context, target *url.URL) (Document, bool, error) {
	headers := make(http.Header)
	headers.Set("Accept", "image/avif,image/webp,image/png,image/jpeg,image/gif,*/*;q=0.8")
	return s.fetchImageWithHeaders(ctx, target, headers)
}

func (s *Service) fetchImageWithHeaders(ctx context.Context, target *url.URL, headers http.Header) (Document, bool, error) {
	head, err := s.client.Do(ctx, http.MethodHead, target.String(), headers, nil)
	if err != nil {
		var coded *CodedError
		if errors.As(err, &coded) && coded.Code == ErrorCodeCrossHostRedirect {
			return Document{}, false, err
		}
		return Document{}, false, nil
	}
	mimeType := imageContentType(head.Header.Get("Content-Type"))
	if !supportedImageTypes[mimeType] {
		return Document{}, false, nil
	}
	if length, ok := declaredContentLength(head.Header.Get("Content-Length")); ok && length > DefaultMaxImageBytes {
		return Document{}, false, imageTooLargeError(target.String(), mimeType, length)
	}

	resp, err := s.client.GetWithMaxBodyBytes(ctx, target.String(), headers, DefaultMaxImageBytes)
	if err != nil {
		var coded *CodedError
		if errors.As(err, &coded) && coded.Code == ErrorCodeResponseTooLarge {
			return Document{}, false, imageTooLargeError(target.String(), mimeType, 0)
		}
		return Document{}, false, nil
	}
	mimeType = imageContentType(resp.Header.Get("Content-Type"))
	if !supportedImageTypes[mimeType] {
		return Document{}, false, nil
	}
	return Document{
		URL:           target.String(),
		FinalURL:      resp.URL,
		StatusCode:    resp.StatusCode,
		ContentType:   mimeType,
		Source:        sourceImage,
		ImageBytes:    resp.Body,
		ImageMimeType: mimeType,
	}, true, nil
}

// fetchImageIfPresent returns an image document for likely image URLs.
func (s *Service) fetchImageIfPresent(ctx context.Context, target *url.URL) (Document, bool, error) {
	if !looksLikeImageURL(target) {
		return Document{}, false, nil
	}
	return s.fetchImageByContentType(ctx, target)
}

func looksLikeImageURL(target *url.URL) bool {
	path := strings.ToLower(target.Path)
	for ext := range supportedImageTypes {
		switch ext {
		case "image/avif":
			if strings.HasSuffix(path, ".avif") {
				return true
			}
		case "image/gif":
			if strings.HasSuffix(path, ".gif") {
				return true
			}
		case "image/jpeg":
			if strings.HasSuffix(path, ".jpg") || strings.HasSuffix(path, ".jpeg") {
				return true
			}
		case "image/png":
			if strings.HasSuffix(path, ".png") {
				return true
			}
		case "image/webp":
			if strings.HasSuffix(path, ".webp") {
				return true
			}
		}
	}
	return false
}

func imageTooLargeError(target, mimeType string, length int64) error {
	message := fmt.Sprintf("image %s exceeds the %d-byte limit for %s", mimeType, DefaultMaxImageBytes, target)
	if length > 0 {
		message = fmt.Sprintf("image %s is %d bytes, exceeding the %d-byte limit for %s", mimeType, length, DefaultMaxImageBytes, target)
	}
	return newCodedError(errors.New(message), ErrorCodeResponseTooLarge, "fetch a smaller image or use a vision-capable higher limit")
}

func imageContentType(header string) string {
	if idx := strings.Index(header, ";"); idx >= 0 {
		header = header[:idx]
	}
	return strings.ToLower(strings.TrimSpace(header))
}

func declaredContentLength(header string) (int64, bool) {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0, false
	}
	length, err := strconv.ParseInt(header, 10, 64)
	return length, err == nil && length >= 0
}
