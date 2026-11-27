-- Create "products" table
CREATE TABLE `products` (
  `id` text NOT NULL,
  `sync_version` integer NOT NULL DEFAULT 0,
  `sync_hlc` text NOT NULL DEFAULT '',
  `sync_clocks` json NULL,
  `tenant_id` text NOT NULL,
  `deleted_at` datetime NULL,
  `name` text NOT NULL DEFAULT '',
  `sku` text NOT NULL DEFAULT '',
  `price` integer NOT NULL DEFAULT 0,
  `stock` real NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`)
);
-- Create index "product_tenant_id" to table: "products"
CREATE INDEX `product_tenant_id` ON `products` (`tenant_id`);
-- Create index "product_deleted_at" to table: "products"
CREATE INDEX `product_deleted_at` ON `products` (`deleted_at`);
-- Create "sales" table
CREATE TABLE `sales` (
  `id` text NOT NULL,
  `sync_version` integer NOT NULL DEFAULT 0,
  `sync_hlc` text NOT NULL DEFAULT '',
  `sync_clocks` json NULL,
  `tenant_id` text NOT NULL,
  `deleted_at` datetime NULL,
  `total` integer NOT NULL DEFAULT 0,
  `sold_at` datetime NULL,
  `note` text NOT NULL DEFAULT '',
  PRIMARY KEY (`id`)
);
-- Create index "sale_tenant_id" to table: "sales"
CREATE INDEX `sale_tenant_id` ON `sales` (`tenant_id`);
-- Create index "sale_deleted_at" to table: "sales"
CREATE INDEX `sale_deleted_at` ON `sales` (`deleted_at`);
-- Create "sale_lines" table
CREATE TABLE `sale_lines` (
  `id` text NOT NULL,
  `sync_version` integer NOT NULL DEFAULT 0,
  `sync_hlc` text NOT NULL DEFAULT '',
  `sync_clocks` json NULL,
  `tenant_id` text NOT NULL,
  `deleted_at` datetime NULL,
  `sale_id` text NOT NULL DEFAULT '',
  `product_id` text NOT NULL DEFAULT '',
  `qty` real NOT NULL DEFAULT 0,
  `price` integer NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`)
);
-- Create index "saleline_tenant_id" to table: "sale_lines"
CREATE INDEX `saleline_tenant_id` ON `sale_lines` (`tenant_id`);
-- Create index "saleline_deleted_at" to table: "sale_lines"
CREATE INDEX `saleline_deleted_at` ON `sale_lines` (`deleted_at`);
-- Create "sync_devices" table
CREATE TABLE `sync_devices` (
  `id` text NOT NULL,
  `tenant_id` text NOT NULL,
  `user_id` text NULL,
  `name` text NULL,
  `platform` text NULL,
  `app_version` text NULL,
  `public_key` text NULL,
  `cursor_seq` integer NOT NULL DEFAULT 0,
  `created_at` integer NOT NULL,
  `last_push_at` integer NULL,
  `last_pull_at` integer NULL,
  `revoked_at` integer NULL,
  PRIMARY KEY (`id`)
);
-- Create index "sync_devices_tenant_idx" to table: "sync_devices"
CREATE INDEX `sync_devices_tenant_idx` ON `sync_devices` (`tenant_id`);
-- Create "sync_ledger" table
CREATE TABLE `sync_ledger` (
  `id` text NOT NULL,
  `tenant_id` text NOT NULL,
  `mutation_id` text NOT NULL,
  `seq` integer NOT NULL DEFAULT 0,
  `device_id` text NOT NULL,
  `user_id` text NULL,
  `entity` text NOT NULL,
  `entity_id` text NOT NULL,
  `op` text NOT NULL,
  `hlc` text NULL,
  `group_id` text NULL,
  `status` text NOT NULL,
  `reason` text NULL,
  `payload` text NULL,
  `created_at` integer NOT NULL,
  PRIMARY KEY (`id`)
);
-- Create index "sync_ledger_seq_idx" to table: "sync_ledger"
CREATE INDEX `sync_ledger_seq_idx` ON `sync_ledger` (`tenant_id`, `seq`);
-- Create index "sync_ledger_entity_idx" to table: "sync_ledger"
CREATE INDEX `sync_ledger_entity_idx` ON `sync_ledger` (`tenant_id`, `entity`, `entity_id`);
-- Create "sync_tenants" table
CREATE TABLE `sync_tenants` (
  `id` text NOT NULL,
  `seq` integer NOT NULL DEFAULT 0,
  `horizon` integer NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`)
);
