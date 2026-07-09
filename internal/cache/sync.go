package cache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"erickaldama-mail/internal/mailbox"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// SyncPageLimit is the FIXED number of rows Sync pulls from DynamoDB to populate the cache. It is
// deliberately independent of the CLI --count flag (audit M-2): search/list must see the full local
// history, not just the last --count messages.
const SyncPageLimit = 500

// HeaderLister is the subset of *mailbox.Reader that Sync needs (enables a fake in tests).
type HeaderLister interface {
	List(ctx context.Context, mailbox string, limit int32, start map[string]ddbtypes.AttributeValue) ([]mailbox.Header, map[string]ddbtypes.AttributeValue, error)
}

// Sync fetches one page of headers for the mailbox from the lister (DynamoDB) and upserts them into
// the cache within a single transaction. The contentless FTS5 index is kept in sync EXPLICITLY —
// no triggers, so the upsert's trigger-firing (NO VERIFICADO by the SQLite docs) is irrelevant.
//
// CRITICAL (verified empirically): contentless FTS5 tables reject `DELETE FROM ... WHERE rowid`
// ("cannot DELETE from contentless fts5 table"). To replace a row's index entry we MUST use the
// special 'delete' command, and it requires the OLD values currently indexed (sqlite.org/fts5.html
// §6.3: "The values inserted into the other columns must match the values currently stored"). So we
// read the prior sender/subject BEFORE the upsert, issue the 'delete' with those OLD values, then
// insert the NEW values. Order matters: read old → 'delete' old → upsert → insert new.
func (c *Cache) Sync(ctx context.Context, r HeaderLister, mailbox string, limit int32) (int, error) {
	hs, _, err := r.List(ctx, mailbox, limit, nil)
	if err != nil {
		return 0, fmt.Errorf("cache sync list: %w", err)
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("cache sync begin: %w", err)
	}
	defer tx.Rollback() // no-op after Commit

	var maxSK string
	for _, h := range hs {
		// 1. Read the OLD indexed values (if this s3Key already exists) so we can remove its stale
		//    FTS entry with the exact values FTS5 has stored (contentless 'delete' requires this).
		var oldRowid sql.NullInt64
		var oldSender, oldSubject sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT rowid, sender, subject FROM headers WHERE s3Key=?`, h.S3Key).
			Scan(&oldRowid, &oldSender, &oldSubject); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("cache sync read old %s: %w", h.S3Key, err)
		} // ErrNoRows → oldRowid.Valid=false (new row); any other error aborts the tx

		// 2. Remove the stale FTS entry via the special 'delete' command (contentless-safe).
		if oldRowid.Valid {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO headers_fts(headers_fts, rowid, sender, subject) VALUES('delete', ?, ?, ?)`,
				oldRowid.Int64, oldSender.String, oldSubject.String); err != nil {
				return 0, fmt.Errorf("cache sync fts delete: %w", err)
			}
		}

		// 3. Upsert the header row, capturing the (stable) rowid.
		var rowid int64
		err := tx.QueryRowContext(ctx, `
			INSERT INTO headers (s3Key, pk, sk, messageId, sender, subject, date)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(s3Key) DO UPDATE SET
				pk=excluded.pk, sk=excluded.sk, messageId=excluded.messageId,
				sender=excluded.sender, subject=excluded.subject, date=excluded.date
			RETURNING rowid`,
			h.S3Key, h.PK, h.SK, h.MessageID, h.From, h.Subject, h.Date,
		).Scan(&rowid)
		if err != nil {
			return 0, fmt.Errorf("cache sync upsert %s: %w", h.S3Key, err)
		}

		// 4. Insert the current FTS entry.
		if _, err := tx.ExecContext(ctx, `INSERT INTO headers_fts(rowid, sender, subject) VALUES (?, ?, ?)`, rowid, h.From, h.Subject); err != nil {
			return 0, fmt.Errorf("cache sync fts insert: %w", err)
		}
		if h.SK > maxSK {
			maxSK = h.SK
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sync_state (mailbox, last_sk, synced_at) VALUES (?, ?, ?)
		ON CONFLICT(mailbox) DO UPDATE SET last_sk=excluded.last_sk, synced_at=excluded.synced_at`,
		mailbox, maxSK, time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		return 0, fmt.Errorf("cache sync state: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("cache sync commit: %w", err)
	}
	return len(hs), nil
}
