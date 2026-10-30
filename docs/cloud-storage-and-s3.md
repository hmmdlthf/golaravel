# Cloud Filesystem & Presigned URLs (pkg/storage) (PRD §7) ☁️

GoLaravel features a high-performance, multi-disk cloud filesystem modeled after Laravel's Flysystem architecture, re-engineered for Go's compiled speed, zero-reflection streaming, and minimal memory footprint.

Unlike traditional Go solutions that pull in massive 100MB+ AWS SDK bundles, GoLaravel includes a **zero-external-dependency AWS Signature Version 4 (SigV4) engine** written entirely in pure Go standard library (`crypto/hmac`, `crypto/sha256`, `net/http`). It natively supports **AWS S3, MinIO, Cloudflare R2, Google Cloud Storage (S3 API), DigitalOcean Spaces, and LocalStack** out-of-the-box.

---

## 🚀 Key Capabilities

- **Multi-Disk Registry**: Switch seamlessly between `local`, `public`, `s3`, `minio`, or custom disk drivers via `Storage.Disk("name")`.
- **Zero-SDK SigV4 Engine**: Pure standard library HMAC-SHA256 request and query signing. Eliminates dozens of external packages and gigabytes of build cache.
- **Time-Limited Presigned URLs**:
  - `TemporaryURL(path, ttl)`: SigV4 query-authenticated download links for private objects.
  - `PresignedPutURL(path, ttl)`: SigV4 query-authenticated direct client upload URLs (bypassing backend web servers).
- **Streaming & Memory Efficiency**: `PutReader(path, r)` and `GetStream(path)` stream multi-gigabyte files directly to and from disks with constant low memory usage.
- **Local Signed URLs**: Local filesystem disk supports cryptographic HMAC-SHA256 temporary URL generation and verification.
- **Multipart Form Upload Helpers**: First-class `c.FormFile(field)` and `c.SaveUploadedFile(file, dst)` on `*context.Context`.

---

## ⚙️ Configuration (`.env`)

Configure your default filesystem disk and cloud credentials in `.env`:

```env
# Default Filesystem Disk: local, public, s3, minio
FILESYSTEM_DISK=s3

# Local Storage Paths
STORAGE_PATH=storage/app
STORAGE_URL=/storage

# AWS S3 / MinIO / Cloudflare R2 Configuration
AWS_ACCESS_KEY_ID=your-access-key-id
AWS_SECRET_ACCESS_KEY=your-secret-access-key
AWS_DEFAULT_REGION=us-east-1
AWS_BUCKET=my-app-bucket
AWS_ENDPOINT=                      # Leave empty for standard AWS S3, or set e.g. http://127.0.0.1:9000 for MinIO
AWS_USE_PATH_STYLE=false           # Set to true for MinIO, LocalStack, and Ceph
AWS_URL=https://cdn.example.com    # Optional public CDN domain prefix
```

---

## 📦 Multi-Disk Usage

### 1. Default Disk & Named Disks

```go
import "github.com/golaravel/golaravel"

// Operates on default disk (FILESYSTEM_DISK)
err := golaravel.Storage.Put("invoices/inv_101.pdf", pdfBytes)

// Target a specific disk explicitly
localDisk := golaravel.Storage.Disk("local")
s3Disk    := golaravel.Storage.Disk("s3")
minioDisk := golaravel.Storage.Disk("minio")

s3Disk.Put("backups/db.tar.gz", archiveData)
```

### 2. Registering Custom Disks

You can dynamically register custom disk drivers at startup:

```go
customDisk, _ := storage.NewS3Driver(storage.S3Config{
    Bucket:       "client-archive",
    Region:       "eu-central-1",
    AccessKey:    os.Getenv("ARCHIVE_KEY"),
    SecretKey:    os.Getenv("ARCHIVE_SECRET"),
    UsePathStyle: false,
})

golaravel.Storage.RegisterDisk("archive", customDisk)
```

---

## 🛠️ Complete Storage Operations

All storage drivers implement the standard `storage.Driver` interface:

```go
// 1. Storing & Retrieving
err := golaravel.Storage.Put("reports/sales.csv", data)
data, err := golaravel.Storage.Get("reports/sales.csv")

// 2. High-Performance Streaming
stream, err := golaravel.Storage.GetStream("videos/intro.mp4")
defer stream.Close()
io.Copy(c.Response, stream)

// 3. Existence & Deletion
if golaravel.Storage.Exists("reports/sales.csv") {
    golaravel.Storage.Delete("reports/sales.csv")
}

// 4. File Metadata
size, _    := golaravel.Storage.Size("reports/sales.csv")
modTime, _ := golaravel.Storage.LastModified("reports/sales.csv")
mimeType, _:= golaravel.Storage.MimeType("reports/sales.csv") // e.g. "text/csv"

// 5. Server-Side Copy & Move
golaravel.Storage.Copy("reports/sales.csv", "backup/sales.csv")
golaravel.Storage.Move("backup/sales.csv", "archive/sales_2026.csv")

// 6. Public URLs
url := golaravel.Storage.URL("avatars/user_1.png")
// S3: https://my-app-bucket.s3.us-east-1.amazonaws.com/avatars/user_1.png
// Local: /storage/avatars/user_1.png
```

---

## 🔐 Presigned URLs (SigV4 & HMAC)

### 1. Temporary Download URLs (`TemporaryURL`)

Generate secure, expiring URLs that allow authorized users to download private files directly from S3, MinIO, or Cloudflare R2 without passing bytes through your application server:

```go
// Generate 30-minute presigned download link
downloadURL, err := golaravel.Storage.TemporaryURL("contracts/nda_signed.pdf", 30*time.Minute)
if err != nil {
    return c.String(http.StatusInternalServerError, "Failed to sign download link")
}

return c.JSON(http.StatusOK, map[string]string{
    "download_url": downloadURL,
})
```

The resulting S3 URL includes AWS Signature Version 4 parameters:
`https://my-bucket.s3.us-east-1.amazonaws.com/contracts/nda_signed.pdf?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=...&X-Amz-Date=20261030T180000Z&X-Amz-Expires=1800&X-Amz-SignedHeaders=host&X-Amz-Signature=...`

### 2. Direct Client Uploads (`PresignedPutURL`)

Eliminate server memory pressure and CPU overhead by letting mobile (Flutter), web (Inertia/Vue/React), or desktop apps upload large assets (e.g. 5GB video files) directly to S3 or MinIO:

```go
// 1. Controller provides presigned upload URL
app.POST("/api/uploads/presign", func(c *golaravel.Context) error {
    filename := fmt.Sprintf("raw-recordings/%s.mov", uuid.NewString())
    
    uploadURL, err := golaravel.Storage.Disk("s3").PresignedPutURL(filename, 15*time.Minute)
    if err != nil {
        return c.String(http.StatusInternalServerError, "Signing failed")
    }

    return c.JSON(http.StatusOK, map[string]string{
        "upload_url": uploadURL,
        "path":       filename,
    })
})
```

```dart
// 2. Flutter client uploads directly to S3 via HTTP PUT:
final response = await http.put(
  Uri.parse(uploadUrl),
  headers: {'Content-Type': 'video/quicktime'},
  body: fileBytes,
);
```

### 3. Local Temporary URL Verification

When using the `local` driver, `TemporaryURL` generates cryptographic HMAC-SHA256 tokens validated via `VerifySignedURL`:

```go
app.GET("/storage/signed/*path", func(c *golaravel.Context) error {
    localDisk := golaravel.Storage.Disk("local").(*storage.LocalDriver)
    
    // Validate signature and expiration
    if err := localDisk.VerifySignedURL(c.Request.URL.String()); err != nil {
        return c.String(http.StatusForbidden, "Invalid or expired signed URL")
    }

    filePath := c.Param("path")
    stream, _ := localDisk.GetStream(filePath)
    defer stream.Close()
    
    return io.Copy(c.Response, stream)
})
```

---

## 📤 Multipart Form Uploads

GoLaravel provides convenient HTTP upload helpers directly on `*golaravel.Context`:

```go
app.POST("/user/avatar", func(c *golaravel.Context) error {
    // 1. Extract uploaded file from multipart form
    file, err := c.FormFile("avatar")
    if err != nil {
        return c.String(http.StatusBadRequest, "No file uploaded")
    }

    // 2. Save directly to local filesystem
    // err = c.SaveUploadedFile(file, "storage/app/public/avatars/" + file.Filename)

    // 3. Or save directly to any Storage disk (S3, MinIO, or Local)
    dstPath := fmt.Sprintf("avatars/%d_%s", c.Get("user_id"), file.Filename)
    err = golaravel.Storage.SaveUploadedFile(file, dstPath, "s3")
    if err != nil {
        return c.String(http.StatusInternalServerError, "Upload to cloud failed")
    }

    return c.JSON(http.StatusOK, map[string]string{
        "url": golaravel.Storage.Disk("s3").URL(dstPath),
    })
})
```

---

## 🧪 Comprehensive Unit & Mock Tests

Run the storage test suite verifying both local and mock S3 servers with SigV4 verification:

```bash
go test -v ./pkg/storage
```
Output:
```
=== RUN   TestStorage_LocalDriver
--- PASS: TestStorage_LocalDriver (0.00s)
=== RUN   TestStorage_S3Driver_And_SigV4
--- PASS: TestStorage_S3Driver_And_SigV4 (0.00s)
=== RUN   TestStorage_DiskManager_And_Uploads
--- PASS: TestStorage_DiskManager_And_Uploads (0.00s)
PASS
ok  	github.com/golaravel/golaravel/pkg/storage	0.007s
```
