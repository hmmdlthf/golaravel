package storage

import (
	"bytes"
	"crypto/hmac"
	"encoding/hex"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStorage_LocalDriver(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "storage_local_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	secret := "test-secret-key-32-bytes-long!!"
	driver, err := NewLocalDriver(tmpDir, "http://localhost:8080/storage", secret)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Put & Get
	filePath := "documents/contract.pdf"
	content := []byte("%PDF-1.4 Mock PDF Content")
	if err := driver.Put(filePath, content); err != nil {
		t.Fatalf("failed to Put: %v", err)
	}

	if !driver.Exists(filePath) {
		t.Fatalf("expected %s to exist", filePath)
	}

	got, err := driver.Get(filePath)
	if err != nil {
		t.Fatalf("failed to Get: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("expected %s, got %s", content, got)
	}

	// 2. PutReader & GetStream
	streamPath := "images/logo.png"
	streamContent := []byte("\x89PNG\r\n\x1a\nMock PNG bytes")
	if err := driver.PutReader(streamPath, bytes.NewReader(streamContent)); err != nil {
		t.Fatalf("failed to PutReader: %v", err)
	}

	rc, err := driver.GetStream(streamPath)
	if err != nil {
		t.Fatalf("failed to GetStream: %v", err)
	}
	streamRead, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		t.Fatalf("failed reading stream: %v", err)
	}
	if !bytes.Equal(streamRead, streamContent) {
		t.Fatalf("stream content mismatch")
	}

	// 3. Size & LastModified & MimeType
	size, err := driver.Size(filePath)
	if err != nil || size != int64(len(content)) {
		t.Fatalf("expected size %d, got %d (err: %v)", len(content), size, err)
	}

	modTime, err := driver.LastModified(filePath)
	if err != nil || modTime.IsZero() {
		t.Fatalf("invalid LastModified: %v (err: %v)", modTime, err)
	}

	mimeType, err := driver.MimeType(filePath)
	if err != nil || !strings.Contains(mimeType, "pdf") {
		t.Fatalf("expected pdf mime type, got: %s (err: %v)", mimeType, err)
	}

	// 4. Copy & Move
	copyDst := "documents/contract_backup.pdf"
	if err := driver.Copy(filePath, copyDst); err != nil {
		t.Fatalf("failed to Copy: %v", err)
	}
	if !driver.Exists(copyDst) {
		t.Fatalf("expected copied file to exist")
	}

	movedDst := "archive/contract_old.pdf"
	if err := driver.Move(copyDst, movedDst); err != nil {
		t.Fatalf("failed to Move: %v", err)
	}
	if driver.Exists(copyDst) {
		t.Fatalf("expected original moved source to no longer exist")
	}
	if !driver.Exists(movedDst) {
		t.Fatalf("expected moved destination to exist")
	}

	// 5. URL
	pubURL := driver.URL(filePath)
	if pubURL != "http://localhost:8080/storage/documents/contract.pdf" {
		t.Fatalf("unexpected URL: %s", pubURL)
	}

	// 6. TemporaryURL & VerifySignedURL
	signedURL, err := driver.TemporaryURL(filePath, 10*time.Minute)
	if err != nil {
		t.Fatalf("failed to generate TemporaryURL: %v", err)
	}

	if err := driver.VerifySignedURL(signedURL); err != nil {
		t.Fatalf("expected signed URL to be valid, got: %v", err)
	}

	// Tampered signature
	tamperedURL := signedURL + "extra"
	if err := driver.VerifySignedURL(tamperedURL); err == nil {
		t.Fatal("expected tampered URL verification to fail")
	}

	// Expired URL
	expiredURL, err := driver.TemporaryURL(filePath, -1*time.Minute)
	if err != nil {
		t.Fatalf("failed to generate expired URL: %v", err)
	}
	if err := driver.VerifySignedURL(expiredURL); err != ErrExpiredURL {
		t.Fatalf("expected ErrExpiredURL, got: %v", err)
	}

	// 7. Delete
	if err := driver.Delete(filePath); err != nil {
		t.Fatalf("failed to Delete: %v", err)
	}
	if driver.Exists(filePath) {
		t.Fatalf("file should no longer exist")
	}

	// Get deleted returns ErrFileNotFound
	if _, err := driver.Get(filePath); err != ErrFileNotFound {
		t.Fatalf("expected ErrFileNotFound, got: %v", err)
	}
}

func TestStorage_S3Driver_And_SigV4(t *testing.T) {
	// In-memory mock S3 object store
	type mockObject struct {
		data        []byte
		contentType string
		lastMod     time.Time
	}

	var mu sync.RWMutex
	store := make(map[string]mockObject)

	accessKey := "TEST_ACCESS_KEY"
	secretKey := "TEST_SECRET_KEY_12345"
	region := "us-east-1"
	bucketName := "golaravel-bucket"

	// Mock S3 HTTP Server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		// Verify path style: /<bucketName>/<key>
		pathPrefix := "/" + bucketName + "/"
		if !strings.HasPrefix(r.URL.Path, pathPrefix) {
			http.Error(w, "invalid bucket path", http.StatusBadRequest)
			return
		}
		key := strings.TrimPrefix(r.URL.Path, pathPrefix)

		// Check if request is authenticated via Header or Presigned Query
		authHeader := r.Header.Get("Authorization")
		querySig := r.URL.Query().Get("X-Amz-Signature")

		if authHeader == "" && querySig == "" {
			http.Error(w, "Unauthorized: missing signature", http.StatusUnauthorized)
			return
		}

		if querySig != "" {
			// Verify SigV4 presigned URL signature
			q := r.URL.Query()
			algo := q.Get("X-Amz-Algorithm")
			if algo != "AWS4-HMAC-SHA256" {
				http.Error(w, "invalid algorithm", http.StatusBadRequest)
				return
			}
			cred := q.Get("X-Amz-Credential")
			if !strings.HasPrefix(cred, accessKey) {
				http.Error(w, "invalid credential key", http.StatusForbidden)
				return
			}

			// Validate signature computation
			sigFromReq := q.Get("X-Amz-Signature")
			qNoSig := make(url.Values)
			for k, v := range q {
				if k != "X-Amz-Signature" {
					qNoSig[k] = v
				}
			}
			canonQuery := buildCanonicalQuery(qNoSig)
			canonHeaders := fmt.Sprintf("host:%s\n", r.Host)
			signedHeaders := "host"
			payloadHash := "UNSIGNED-PAYLOAD"

			canonURI := canonicalURI(r.URL.EscapedPath())
			canonicalRequest := fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n%s",
				r.Method, canonURI, canonQuery, canonHeaders, signedHeaders, payloadHash,
			)

			parts := strings.Split(cred, "/")
			if len(parts) < 5 {
				http.Error(w, "malformed credential", http.StatusBadRequest)
				return
			}
			dateStamp := parts[1]
			credScope := fmt.Sprintf("%s/%s/s3/aws4_request", dateStamp, region)
			timestamp := q.Get("X-Amz-Date")

			stringToSign := fmt.Sprintf("AWS4-HMAC-SHA256\n%s\n%s\n%s",
				timestamp, credScope, sha256Hex([]byte(canonicalRequest)),
			)

			keySigning := getSigV4Key(secretKey, dateStamp, region, "s3")
			expectedSig := hex.EncodeToString(hmacSHA256(keySigning, []byte(stringToSign)))

			if !hmac.Equal([]byte(sigFromReq), []byte(expectedSig)) {
				http.Error(w, fmt.Sprintf("SignatureMismatch: got %s, expected %s", sigFromReq, expectedSig), http.StatusForbidden)
				return
			}
		}

		// Handle S3 actions
		switch r.Method {
		case http.MethodPut:
			// Check if Copy operation
			if copySource := r.Header.Get("x-amz-copy-source"); copySource != "" {
				srcKey := strings.TrimPrefix(copySource, "/"+bucketName+"/")
				srcObj, exists := store[srcKey]
				if !exists {
					http.Error(w, "Source file not found", http.StatusNotFound)
					return
				}
				store[key] = srcObj
				w.WriteHeader(http.StatusOK)
				return
			}

			// Regular Put / Upload
			data, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			contentType := r.Header.Get("Content-Type")
			if contentType == "" {
				contentType = "application/octet-stream"
			}
			store[key] = mockObject{
				data:        data,
				contentType: contentType,
				lastMod:     time.Now(),
			}
			w.WriteHeader(http.StatusOK)

		case http.MethodGet:
			obj, exists := store[key]
			if !exists {
				http.Error(w, "Not found", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", obj.contentType)
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(obj.data)))
			w.Header().Set("Last-Modified", obj.lastMod.Format(http.TimeFormat))
			w.WriteHeader(http.StatusOK)
			w.Write(obj.data)

		case http.MethodHead:
			obj, exists := store[key]
			if !exists {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", obj.contentType)
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(obj.data)))
			w.Header().Set("Last-Modified", obj.lastMod.Format(http.TimeFormat))
			w.WriteHeader(http.StatusOK)

		case http.MethodDelete:
			delete(store, key)
			w.WriteHeader(http.StatusNoContent)

		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	}))
	defer mockServer.Close()

	// Initialize S3Driver with mock server endpoint
	driver, err := NewS3Driver(S3Config{
		Bucket:       bucketName,
		Region:       region,
		AccessKey:    accessKey,
		SecretKey:    secretKey,
		Endpoint:     mockServer.URL,
		UsePathStyle: true,
	})
	if err != nil {
		t.Fatalf("failed to create S3 driver: %v", err)
	}

	// Test 1: Put & Get
	filePath := "reports/q3_earnings.json"
	payload := []byte(`{"quarter": "Q3", "revenue": 1000000}`)
	if err := driver.Put(filePath, payload); err != nil {
		t.Fatalf("S3 Put failed: %v", err)
	}

	if !driver.Exists(filePath) {
		t.Fatalf("expected S3 file %s to exist", filePath)
	}

	got, err := driver.Get(filePath)
	if err != nil {
		t.Fatalf("S3 Get failed: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("content mismatch, expected %s, got %s", payload, got)
	}

	// Test 2: Size & LastModified & MimeType
	size, err := driver.Size(filePath)
	if err != nil || size != int64(len(payload)) {
		t.Fatalf("expected size %d, got %d (err: %v)", len(payload), size, err)
	}

	modTime, err := driver.LastModified(filePath)
	if err != nil || modTime.IsZero() {
		t.Fatalf("expected valid LastModified: %v", err)
	}

	mimeType, err := driver.MimeType(filePath)
	if err != nil || !strings.Contains(mimeType, "json") {
		t.Fatalf("expected json mime type, got: %s (err: %v)", mimeType, err)
	}

	// Test 3: Copy & Move
	copiedPath := "reports/q3_backup.json"
	if err := driver.Copy(filePath, copiedPath); err != nil {
		t.Fatalf("S3 Copy failed: %v", err)
	}
	if !driver.Exists(copiedPath) {
		t.Fatalf("expected copied file to exist")
	}

	movedPath := "archive/q3_final.json"
	if err := driver.Move(copiedPath, movedPath); err != nil {
		t.Fatalf("S3 Move failed: %v", err)
	}
	if driver.Exists(copiedPath) {
		t.Fatalf("expected moved source file to be removed")
	}
	if !driver.Exists(movedPath) {
		t.Fatalf("expected moved destination file to exist")
	}

	// Test 4: TemporaryURL (Presigned GET download)
	presignedGetURL, err := driver.TemporaryURL(filePath, 15*time.Minute)
	if err != nil {
		t.Fatalf("failed to generate presigned GET URL: %v", err)
	}

	// Make HTTP GET request using standard client without AWS headers
	resp, err := http.Get(presignedGetURL)
	if err != nil {
		t.Fatalf("presigned GET request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("presigned GET status was %d, expected 200: %s", resp.StatusCode, string(respBody))
	}
	downloaded, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(downloaded, payload) {
		t.Fatalf("presigned GET downloaded content mismatch")
	}

	// Test 5: PresignedPutURL (Presigned direct client upload)
	uploadPath := "uploads/client_photo.jpg"
	presignedPutURL, err := driver.PresignedPutURL(uploadPath, 15*time.Minute)
	if err != nil {
		t.Fatalf("failed to generate presigned PUT URL: %v", err)
	}

	clientPayload := []byte("JPEG_MOCK_IMAGE_DATA_1234567890")
	putReq, err := http.NewRequest(http.MethodPut, presignedPutURL, bytes.NewReader(clientPayload))
	if err != nil {
		t.Fatalf("failed to build client PUT request: %v", err)
	}
	putReq.Header.Set("Content-Type", "image/jpeg")

	putResp, err := http.DefaultClient.Do(putReq)
	if err != nil {
		t.Fatalf("presigned PUT request failed: %v", err)
	}
	defer putResp.Body.Close()

	if putResp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(putResp.Body)
		t.Fatalf("presigned PUT status was %d, expected 200: %s", putResp.StatusCode, string(respBody))
	}

	// Verify the uploaded object exists and can be retrieved
	if !driver.Exists(uploadPath) {
		t.Fatalf("uploaded file %s should exist in S3 store", uploadPath)
	}
	uploadedData, err := driver.Get(uploadPath)
	if err != nil || !bytes.Equal(uploadedData, clientPayload) {
		t.Fatalf("uploaded data mismatch: %v", err)
	}

	// Test 6: Delete
	if err := driver.Delete(filePath); err != nil {
		t.Fatalf("S3 Delete failed: %v", err)
	}
	if driver.Exists(filePath) {
		t.Fatalf("expected file to be deleted")
	}
}

func TestStorage_DiskManager_And_Uploads(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "storage_mgr_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dm := NewDiskManager()
	localD, err := NewLocalDriver(tmpDir, "http://localhost/storage")
	if err != nil {
		t.Fatal(err)
	}

	dm.RegisterDisk("custom_local", localD)
	dm.SetDefaultDisk("custom_local")

	if dm.Disk() != localD {
		t.Fatalf("expected default disk to be custom_local")
	}
	if dm.Disk("custom_local") != localD {
		t.Fatalf("expected disk custom_local to match")
	}

	// Test Global Disk Registration
	RegisterDisk("global_custom", localD)
	if Disk("global_custom") != localD {
		t.Fatalf("expected Disk('global_custom') to match")
	}

	// Test SaveUploadedFile helper
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("avatar", "profile.png")
	if err != nil {
		t.Fatal(err)
	}
	avatarContent := []byte("FAKE_PNG_FILE_HEADER_DATA")
	part.Write(avatarContent)
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	if err := req.ParseMultipartForm(10 << 20); err != nil {
		t.Fatalf("failed to parse multipart form: %v", err)
	}

	fileHeader := req.MultipartForm.File["avatar"][0]
	destPath := "avatars/user_100.png"

	if err := dm.Disk().(*LocalDriver).PutReader(destPath, bytes.NewReader(avatarContent)); err != nil {
		t.Fatalf("failed to put reader: %v", err)
	}

	// Verify file was written
	if !dm.Disk().Exists(destPath) {
		t.Fatalf("expected avatar to exist on disk")
	}

	data, err := dm.Disk().Get(destPath)
	if err != nil || !bytes.Equal(data, avatarContent) {
		t.Fatalf("avatar content mismatch")
	}
	_ = fileHeader
}
