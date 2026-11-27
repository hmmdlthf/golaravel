package schema

import offline "github.com/golaravel/golaravel/pkg/offline/schema"

// The offline sync engine's tables, managed by Ent (and Atlas) like the
// rest of the schema. Engine.Migrate can create them instead.
type (
	SyncLedger struct{ offline.SyncLedger }
	SyncDevice struct{ offline.SyncDevice }
	SyncTenant struct{ offline.SyncTenant }
)
