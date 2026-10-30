package storage

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	sigV4Algorithm = "AWS4-HMAC-SHA256"
	iso8601Format  = "20060102T150405Z"
	dateStampFormat = "20060102"
)

// rfc3986Encode encodes a string according to RFC 3986.
func rfc3986Encode(s string) string {
	encoded := url.QueryEscape(s)
	encoded = strings.ReplaceAll(encoded, "+", "%20")
	encoded = strings.ReplaceAll(encoded, "%7E", "~")
	return encoded
}

// canonicalURI normalizes and encodes the URI path for SigV4.
func canonicalURI(path string) string {
	if path == "" || path == "/" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	segments := strings.Split(path, "/")
	var encoded []string
	for _, seg := range segments {
		encoded = append(encoded, rfc3986Encode(seg))
	}
	res := strings.Join(encoded, "/")
	if !strings.HasPrefix(res, "/") {
		res = "/" + res
	}
	return res
}

// hmacSHA256 computes HMAC-SHA256 checksum.
func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

// sha256Hex computes hex-encoded SHA-256 hash.
func sha256Hex(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

// getSigV4Key derives the AWS Signature Version 4 signing key.
func getSigV4Key(secretKey, dateStamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secretKey), []byte(dateStamp))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))
	return kSigning
}

// SignS3Request signs an outgoing HTTP request with AWS SigV4 Authorization headers.
func SignS3Request(req *http.Request, payload []byte, accessKey, secretKey, region, service string, signTime time.Time) {
	timestamp := signTime.UTC().Format(iso8601Format)
	dateStamp := signTime.UTC().Format(dateStampFormat)

	req.Header.Set("x-amz-date", timestamp)

	payloadHash := sha256Hex(payload)
	req.Header.Set("x-amz-content-sha256", payloadHash)

	// Collect and sort headers to sign
	headersToSign := map[string]string{
		"host":                 req.URL.Host,
		"x-amz-date":           timestamp,
		"x-amz-content-sha256": payloadHash,
	}

	for k, v := range req.Header {
		lower := strings.ToLower(k)
		if strings.HasPrefix(lower, "x-amz-") {
			headersToSign[lower] = strings.TrimSpace(strings.Join(v, ","))
		}
	}

	var headerKeys []string
	for k := range headersToSign {
		headerKeys = append(headerKeys, k)
	}
	sort.Strings(headerKeys)

	var canonicalHeaders strings.Builder
	for _, k := range headerKeys {
		canonicalHeaders.WriteString(fmt.Sprintf("%s:%s\n", k, headersToSign[k]))
	}
	signedHeaders := strings.Join(headerKeys, ";")

	// Canonical query
	canonicalQuery := buildCanonicalQuery(req.URL.Query())

	// Canonical request
	canonURI := canonicalURI(req.URL.EscapedPath())
	if canonURI == "" {
		canonURI = "/"
	}

	canonicalRequest := fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n%s",
		req.Method,
		canonURI,
		canonicalQuery,
		canonicalHeaders.String(),
		signedHeaders,
		payloadHash,
	)

	credentialScope := fmt.Sprintf("%s/%s/%s/aws4_request", dateStamp, region, service)
	stringToSign := fmt.Sprintf("%s\n%s\n%s\n%s",
		sigV4Algorithm,
		timestamp,
		credentialScope,
		sha256Hex([]byte(canonicalRequest)),
	)

	signingKey := getSigV4Key(secretKey, dateStamp, region, service)
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	authHeader := fmt.Sprintf("%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		sigV4Algorithm,
		accessKey,
		credentialScope,
		signedHeaders,
		signature,
	)
	req.Header.Set("Authorization", authHeader)
}

// PresignS3URL generates a SigV4 presigned URL for GET (download) or PUT (upload).
func PresignS3URL(method string, targetURL *url.URL, accessKey, secretKey, region, service string, ttl time.Duration, signTime time.Time) (string, error) {
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	if ttl > 7*24*time.Hour {
		return "", fmt.Errorf("storage: SigV4 presigned URL TTL cannot exceed 7 days")
	}

	timestamp := signTime.UTC().Format(iso8601Format)
	dateStamp := signTime.UTC().Format(dateStampFormat)
	credentialScope := fmt.Sprintf("%s/%s/%s/aws4_request", dateStamp, region, service)

	q := targetURL.Query()
	q.Set("X-Amz-Algorithm", sigV4Algorithm)
	q.Set("X-Amz-Credential", fmt.Sprintf("%s/%s", accessKey, credentialScope))
	q.Set("X-Amz-Date", timestamp)
	q.Set("X-Amz-Expires", strconv.FormatInt(int64(ttl.Seconds()), 10))
	q.Set("X-Amz-SignedHeaders", "host")

	// Build canonical query
	canonicalQuery := buildCanonicalQuery(q)

	canonURI := canonicalURI(targetURL.EscapedPath())
	if canonURI == "" {
		canonURI = "/"
	}

	canonicalHeaders := fmt.Sprintf("host:%s\n", targetURL.Host)
	signedHeaders := "host"
	payloadHash := "UNSIGNED-PAYLOAD"

	canonicalRequest := fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n%s",
		strings.ToUpper(method),
		canonURI,
		canonicalQuery,
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	)

	stringToSign := fmt.Sprintf("%s\n%s\n%s\n%s",
		sigV4Algorithm,
		timestamp,
		credentialScope,
		sha256Hex([]byte(canonicalRequest)),
	)

	signingKey := getSigV4Key(secretKey, dateStamp, region, service)
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	// Append signature to query
	q.Set("X-Amz-Signature", signature)

	result := *targetURL
	result.RawQuery = q.Encode()
	return result.String(), nil
}

// buildCanonicalQuery constructs a sorted canonical query string.
func buildCanonicalQuery(values url.Values) string {
	if len(values) == 0 {
		return ""
	}
	var keys []string
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var pairs []string
	for _, k := range keys {
		vals := values[k]
		sort.Strings(vals)
		escapedKey := rfc3986Encode(k)
		for _, v := range vals {
			pairs = append(pairs, fmt.Sprintf("%s=%s", escapedKey, rfc3986Encode(v)))
		}
	}
	return strings.Join(pairs, "&")
}
