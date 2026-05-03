package api

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
)

// objectInfo is the dashboard-friendly view of an S3 object.
type objectInfo struct {
	Key          string    `json:"key"`
	Size         int64     `json:"size"`
	ETag         string    `json:"etag,omitempty"`
	LastModified time.Time `json:"lastModified"`
	ContentType  string    `json:"contentType,omitempty"`
}

// handleListObjects lists objects in a bucket. Supports a `prefix` query
// param for "folder" navigation.
func (s *Server) handleListObjects(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("name")
	cl, err := s.s3Client(r.Context())
	if err != nil {
		httpError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	prefix := r.URL.Query().Get("prefix")
	out := make([]objectInfo, 0, 64)
	ch := cl.ListObjects(r.Context(), bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: r.URL.Query().Get("recursive") == "true",
	})
	for o := range ch {
		if o.Err != nil {
			httpError(w, http.StatusBadGateway, o.Err.Error())
			return
		}
		out = append(out, objectInfo{
			Key:          o.Key,
			Size:         o.Size,
			ETag:         o.ETag,
			LastModified: o.LastModified,
			ContentType:  o.ContentType,
		})
	}
	writeJSON(w, 200, out)
}

// handlePutObject uploads (or overwrites) an object. The object key is
// derived from a `key` query parameter; the body is the object content.
func (s *Server) handlePutObject(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("name")
	key := r.URL.Query().Get("key")
	if key == "" {
		httpError(w, 400, "missing key query param")
		return
	}
	if strings.HasPrefix(key, "/") {
		key = strings.TrimPrefix(key, "/")
	}
	cl, err := s.s3Client(r.Context())
	if err != nil {
		httpError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	contentType := r.Header.Get("content-type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	info, err := cl.PutObject(r.Context(), bucket, key, r.Body, r.ContentLength,
		minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, 201, objectInfo{
		Key:          info.Key,
		Size:         info.Size,
		ETag:         info.ETag,
		LastModified: time.Now().UTC(),
		ContentType:  contentType,
	})
}

// handleGetObject streams the object body to the response.
func (s *Server) handleGetObject(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("name")
	key := r.URL.Query().Get("key")
	if key == "" {
		httpError(w, 400, "missing key query param")
		return
	}
	cl, err := s.s3Client(r.Context())
	if err != nil {
		httpError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	obj, err := cl.GetObject(r.Context(), bucket, key, minio.GetObjectOptions{})
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer obj.Close()
	stat, err := obj.Stat()
	if err != nil {
		var rerr minio.ErrorResponse
		if errors.As(err, &rerr) && rerr.StatusCode == http.StatusNotFound {
			httpError(w, http.StatusNotFound, "object not found")
			return
		}
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	if stat.ContentType != "" {
		w.Header().Set("content-type", stat.ContentType)
	}
	if stat.Size > 0 {
		w.Header().Set("content-length", itoa(stat.Size))
	}
	if !stat.LastModified.IsZero() {
		w.Header().Set("last-modified", stat.LastModified.UTC().Format(http.TimeFormat))
	}
	if stat.ETag != "" {
		w.Header().Set("etag", stat.ETag)
	}
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("content-disposition", `attachment; filename="`+lastPath(key)+`"`)
	}
	_, _ = io.Copy(w, obj)
}

// handleDeleteObject removes an object.
func (s *Server) handleDeleteObject(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("name")
	key := r.URL.Query().Get("key")
	if key == "" {
		httpError(w, 400, "missing key query param")
		return
	}
	cl, err := s.s3Client(r.Context())
	if err != nil {
		httpError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	if err := cl.RemoveObject(r.Context(), bucket, key, minio.RemoveObjectOptions{}); err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func itoa(n int64) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = digits[n%10]
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func lastPath(s string) string {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return s[i+1:]
		}
	}
	return s
}
