package app

import (
	"database/sql"
	"encoding/json"
	"errors"
)

// Migration is transactional: either legacy images and copy indexes all migrate, or none do.
func migrateMedia(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err = tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version >= 1 {
		return tx.Commit()
	}
	rows, err := tx.Query("SELECT id,imageData FROM memes")
	if err != nil {
		return err
	}
	type legacy struct{ id, image string }
	values := []legacy{}
	for rows.Next() {
		var v legacy
		if err = rows.Scan(&v.id, &v.image); err != nil {
			rows.Close()
			return err
		}
		values = append(values, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if _, err = tx.Exec("ALTER TABLE memes RENAME COLUMN imageData TO images; ALTER TABLE copies ADD COLUMN imageIndex INTEGER NOT NULL DEFAULT -1; UPDATE copies SET imageIndex=0 WHERE kind='image'"); err != nil {
		return err
	}
	for _, v := range values {
		images := []string{}
		if v.image != "" {
			images = append(images, v.image)
		}
		b, _ := json.Marshal(images)
		if _, err = tx.Exec("UPDATE memes SET images=? WHERE id=?", string(b), v.id); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("PRAGMA user_version=1"); err != nil {
		return err
	}
	return tx.Commit()
}
func decodeImages(raw string) ([]string, error) {
	images := []string{}
	if err := json.Unmarshal([]byte(raw), &images); err != nil {
		return nil, err
	}
	if images == nil {
		images = []string{}
	}
	for _, s := range images {
		if s == "" {
			return nil, errors.New("图片数据为空")
		}
	}
	return images, nil
}
func (a *App) storeMeme(m Meme) error {
	if m.Images == nil {
		m.Images = []string{}
	}
	if m.Text == "" && len(m.Images) == 0 {
		return errors.New("meme 不能没有文字和图片")
	}
	b, err := json.Marshal(m.Images)
	if err != nil {
		return err
	}
	_, err = a.db.Exec("INSERT OR IGNORE INTO memes (id,text,images,channel,url,score,createdAt) VALUES (?,?,?,?,?,?,?)", m.ID, m.Text, string(b), m.Channel, m.URL, m.Score, m.CreatedAt)
	return err
}
