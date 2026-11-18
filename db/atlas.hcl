env "local" {
  // Read the desired schema
  src = "file://schema.sql"

  // Compare schemas in a throwaway PostgreSQL 18 container
  dev = "docker://postgres/18/dev?search_path=public"

  // Write versioned migrations
  migration {
    dir = "file://migrations"
  }
}
