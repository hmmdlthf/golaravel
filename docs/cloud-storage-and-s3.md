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
- **Laravel-style uploads**: `c.File("avatar").Store("avatars", "public")`, `c.Files`, `c.HasFile`, `StoreAs`, `HashName`, and a type and extension detected from the file's bytes.
- **Serving stored files**: `app.ServeStorage()` serves public files to anyone and private files through signed links, with safe download headers.
- **Per-route upload limits**: `middleware.MaxBodySize(n)`.
- **Test fakes**: `gltest.FakeStorage`, `gltest.FakeImage` and `gltest.FakeUpload`.

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

### 3. Serving Local Files (`app.ServeStorage`)

The `local` and `public` disks build links under `/storage`. `app.ServeStorage()` mounts a route that answers them:

```go
app := golaravel.Default()
app.ServeStorage()
```

| Request | Result |
|---|---|
| `GET /storage/public/avatars/a.png` | Served to anyone: the `public` disk is public. |
| `GET /storage/contracts/nda.pdf` | 404: the `local` disk is private. |
| A `TemporaryURL` link | Served while the signature is valid; 403 when tampered with or expired. |
| `PUT` to a `PresignedPutURL` link | Stores the request body (direct uploads); 201. |

Every response sends `X-Content-Type-Options: nosniff` and a sandboxing `Content-Security-Policy`. Images, PDFs, video, audio and plain text are shown inline. Anything a browser could run as a page (HTML, SVG, XML, scripts) is always downloaded. Directories, and paths with a segment starting with `.` (such as `.env`), get 404. Add `?download` or `?download=name.pdf` to any link to download it under that name.

Call `ServeStorage` after registering extra local disks: it mounts every local disk's URL path. Without the engine, `storage.Handler()` is the same handler as an `http.Handler`.

### 4. Visibility

Like Laravel, a disk is **public** (its files may be linked with `URL`) or **private** (they need `TemporaryURL`).

- The `public` disk is public. `local` and other disks are private.
- Change a local disk with `(*storage.LocalDriver).SetVisibility(storage.VisibilityPublic)`.
- An S3 disk is public when `S3Config.Visibility` (env `AWS_VISIBILITY`) is `public`. When that is empty, it is public if `PublicURLPrefix` (`AWS_URL`) is set.

`DisplayURL` picks the right link for the disk:

```go
url, err := golaravel.Storage.DisplayURL("contracts/nda.pdf", 10*time.Minute, "local")
// a signed link on a private disk, the plain URL on a public one
```

---

## 📤 File Uploads

Uploaded files come as `*storage.UploadedFile` (`golaravel.UploadedFile`), which works like Laravel's `UploadedFile`:

```go
app.POST("/user/avatar", func(c *golaravel.Context) error {
    if !c.HasFile("avatar") {
        return c.String(http.StatusUnprocessableEntity, "Choose a picture")
    }
    // random name, guessed extension: "avatars/Xq3...9.png"
    path, err := c.File("avatar").Store("avatars", "public")
    if err != nil {
        return err
    }
    return c.JSON(http.StatusOK, map[string]string{"path": path, "url": golaravel.Storage.Disk("public").URL(path)})
})
```

| Request helper | Returns |
|---|---|
| `c.File("avatar")` | The file sent as `avatar` (or `avatar[]`), or nil. Methods on nil are safe: `Store` returns `storage.ErrNoFile`. |
| `c.Files("photos")` | Every file sent as `photos` or `photos[]` (`<input multiple>`). |
| `c.HasFile("avatar")` | Whether a file was sent. |
| `c.AllFiles()` | All files, keyed by field name. |

| `UploadedFile` method | Laravel | Notes |
|---|---|---|
| `Store(dir, disk...)` | `store()` | Random 40 character name; returns the path. |
| `StoreAs(dir, name, disk...)` | `storeAs()` | `..` segments are removed. |
| `StorePublicly(dir)` / `StorePubliclyAs(dir, name)` | `storePublicly()` | The `public` disk unless another is given. |
| `HashName(dir...)` | `hashName()` | |
| `ClientOriginalName()` | `getClientOriginalName()` | Without any directory part. Show it to people, never use it as a path. |
| `ClientOriginalExtension()` | `getClientOriginalExtension()` | |
| `ClientMimeType()` | `getClientMimeType()` | What the client claimed. |
| `MimeType()` | `getMimeType()` | Detected from the bytes. |
| `Extension()` | `extension()` | Guessed from the bytes. A PNG named `.exe` gets `png`; text and office files keep their own extension. |
| `Size()`, `IsValid()`, `Open()`, `Bytes()` | | |

Disk-level helpers in the style of Laravel's `Storage::putFile`:

```go
path, _ := golaravel.Storage.PutFile("invoices", file, "s3")
path, _ = golaravel.Storage.PutFileAs("invoices", file, "inv-101.pdf", "s3")
_ = golaravel.Storage.DeleteMany([]string{old1, old2}, "s3") // missing files are ignored
```

Validation rules (`file`, `image`, `mimes`, `mimetypes`, `extensions`, `dimensions`, `max` and so on) accept `*UploadedFile` values as well as multipart headers.

### Upload size

Request bodies are capped at 32 MiB (`app.MaxBodyBytes`). Give one route another limit with `middleware.MaxBodySize`, and a longer read time with `middleware.BodyReadTimeout`:

```go
app.POST("/videos",
    middleware.MaxBodySize(2<<30),               // 2 GiB on this route only
    middleware.BodyReadTimeout(30*time.Minute),
    uploadVideo)
```

A bigger body fails to read: `c.File` returns nil and form parsing returns `*http.MaxBytesError`. Method spoofing (`_method`) reads only the fields before the first file, so large uploads are not parsed before routing.

### Testing uploads

```go
func TestAvatarUpload(t *testing.T) {
    disk := gltest.FakeStorage(t, "public") // in-memory, put back after the test

    res := app.Test(t).Request("POST", "/user/avatar").
        WithUpload("avatar", gltest.FakeImage("me.jpg", 200, 200)).
        Send()

    res.AssertOK()
    disk.AssertCount("avatars", 1)
}
```

- **`gltest.FakeImage(name, w, h)`** makes a real PNG, JPEG or GIF (picked by the extension), so `image` and `dimensions` rules pass.
- **`gltest.FakeUpload(name, kb, mime...)`** makes a file that starts with the right signature for PDF, zip, office, gzip and mp3 files.
- **`.UploadedFile()`** turns either into a `*storage.UploadedFile` for unit tests that don't send a request.
- **Assertions:** `AssertExists`, `AssertMissing`, `AssertCount` and `AssertDirectoryEmpty`.

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
