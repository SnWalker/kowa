# Database migrations

Migration files use paired names:

```text
000001_short_name.up.sql
000001_short_name.down.sql
```

S00 establishes validation only. The first schema migration belongs to the stage that owns its data contract.
