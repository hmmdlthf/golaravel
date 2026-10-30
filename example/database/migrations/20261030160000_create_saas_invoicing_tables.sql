-- Create "invoices" table
CREATE TABLE `invoices` (
  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  `invoice_number` text NOT NULL,
  `customer_name` text NOT NULL,
  `customer_email` text NOT NULL,
  `status` text NOT NULL DEFAULT 'sent',
  `amount` real NOT NULL DEFAULT 0,
  `due_date` datetime NOT NULL,
  `token` text NOT NULL,
  `created_at` datetime NOT NULL,
  `user_invoices` integer NULL,
  CONSTRAINT `invoices_users_invoices` FOREIGN KEY (`user_invoices`) REFERENCES `users` (`id`) ON UPDATE NO ACTION ON DELETE SET NULL
);
-- Create index "invoices_invoice_number_key" to table: "invoices"
CREATE UNIQUE INDEX `invoices_invoice_number_key` ON `invoices` (`invoice_number`);
-- Create index "invoices_token_key" to table: "invoices"
CREATE UNIQUE INDEX `invoices_token_key` ON `invoices` (`token`);
-- Create "line_items" table
CREATE TABLE `line_items` (
  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  `description` text NOT NULL,
  `quantity` integer NOT NULL DEFAULT 1,
  `unit_price` real NOT NULL DEFAULT 0,
  `total` real NOT NULL DEFAULT 0,
  `invoice_items` integer NOT NULL,
  CONSTRAINT `line_items_invoices_items` FOREIGN KEY (`invoice_items`) REFERENCES `invoices` (`id`) ON UPDATE NO ACTION ON DELETE NO ACTION
);
-- Create "users" table
CREATE TABLE `users` (
  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  `name` text NOT NULL,
  `email` text NOT NULL,
  `password` text NOT NULL DEFAULT '',
  `role` text NOT NULL DEFAULT 'member',
  `created_at` datetime NOT NULL
);
-- Create index "users_email_key" to table: "users"
CREATE UNIQUE INDEX `users_email_key` ON `users` (`email`);
