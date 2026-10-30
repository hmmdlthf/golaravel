env "local" {
  src = "ent://example/app/schema"
  dev = "sqlite://file?mode=memory&cache=shared"
  migration {
    dir = "file://example/database/migrations"
  }
}
