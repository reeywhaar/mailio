package migrations

// Junk goes to one account, which is somebody at one domain, so it is a domain's own choice and
// never the default. Domains following a junk default take it as their own, and the default
// becomes reject.
var junkPerDomain = Migration{
	Name: "20261110002144_junk_per_domain",
	Up: exec(`
UPDATE domains
   SET unknown = 'junk', unknown_own = 1,
       unknown_account_id = (SELECT unknown_account_id FROM settings WHERE id = 1)
 WHERE NOT unknown_own AND (SELECT unknown FROM settings WHERE id = 1) = 'junk';

-- Rebuilt, because SQLite drops no column a foreign key or a CHECK names.
CREATE TABLE settings_new (
  id       INTEGER PRIMARY KEY CHECK (id = 1),
  unknown  TEXT NOT NULL DEFAULT 'reject' CHECK (unknown IN ('reject', 'drop')),
  dmarc    TEXT NOT NULL DEFAULT 'reject' CHECK (dmarc IN ('none', 'quarantine', 'reject')),
  spf      TEXT NOT NULL DEFAULT '~all' CHECK (spf IN ('-all', '~all', '?all')),
  relay_id TEXT REFERENCES relays (id)
);
INSERT INTO settings_new (id, unknown, dmarc, spf, relay_id)
SELECT id, CASE unknown WHEN 'junk' THEN 'reject' ELSE unknown END, dmarc, spf, relay_id FROM settings;
DROP TABLE settings;
ALTER TABLE settings_new RENAME TO settings;
`),
}
