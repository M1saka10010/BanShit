package app

import (
	"database/sql"
	"strings"
)

// Preserve existing IDs and foreign keys while preventing reuse after deletion.
// The sequence must also stay above the learning watermark of older databases.
func migrateCopyIDs(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var schema string
	if err = tx.QueryRow("SELECT sql FROM sqlite_master WHERE type='table' AND name='copies'").Scan(&schema); err != nil {
		return err
	}
	if !strings.Contains(strings.ToUpper(schema), "AUTOINCREMENT") {
		_, err = tx.Exec(`CREATE TABLE copies_new (id INTEGER PRIMARY KEY AUTOINCREMENT, memeId TEXT NOT NULL REFERENCES memes(id), kind TEXT NOT NULL, visitor TEXT NOT NULL, createdAt TEXT NOT NULL, imageIndex INTEGER NOT NULL DEFAULT -1);
INSERT INTO copies_new(id,memeId,kind,visitor,createdAt,imageIndex) SELECT id,memeId,kind,visitor,createdAt,imageIndex FROM copies;
DROP TABLE copies;
ALTER TABLE copies_new RENAME TO copies;
CREATE INDEX copies_meme ON copies(memeId);`)
		if err != nil {
			return err
		}
	}
	_, err = tx.Exec(`INSERT INTO sqlite_sequence(name,seq) SELECT 'copies',0 WHERE NOT EXISTS (SELECT 1 FROM sqlite_sequence WHERE name='copies');
UPDATE sqlite_sequence SET seq=max(seq,coalesce((SELECT max(id) FROM copies),0),coalesce((SELECT CAST(value AS INTEGER) FROM meta WHERE key='optimizedCopyId'),0)) WHERE name='copies';`)
	if err != nil {
		return err
	}
	return tx.Commit()
}
