package gormstore

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/greatgoodtiver/matrixrss/internal/domain"
	"github.com/greatgoodtiver/matrixrss/internal/usecase"
)

var _ usecase.MessageRepository = (*MessageRepository)(nil)

type MessageRepository struct {
	db *gorm.DB
}

func (r *MessageRepository) Add(ctx context.Context, msg domain.Message) error {
	model := newMessageModel(msg)
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&model).Error
}

func (r *MessageRepository) Get(ctx context.Context, roomID, eventID string) (domain.Message, error) {
	return r.findOne(r.db.WithContext(ctx).Where("room_id = ? AND event_id = ?", roomID, eventID))
}

func (r *MessageRepository) Oldest(ctx context.Context, roomID string) (domain.Message, error) {
	return r.findOne(r.db.WithContext(ctx).Where("room_id = ?", roomID).Order("sent_at ASC, seq ASC"))
}

func (r *MessageRepository) List(ctx context.Context, roomID string, query domain.MessageQuery) ([]domain.Message, error) {
	tx := r.db.WithContext(ctx).Where("room_id = ?", roomID)
	if before := query.Before; before != nil {
		tx = tx.Where("sent_at < ? OR (sent_at = ? AND seq < ?)", before.SentAt, before.SentAt, before.Seq)
	}
	switch query.Deleted {
	case domain.ExcludeDeleted:
		tx = tx.Where("deleted_at IS NULL")
	case domain.OnlyDeleted:
		tx = tx.Where("deleted_at IS NOT NULL")
	}

	var models []messageModel
	if err := tx.Order("sent_at DESC, seq DESC").Limit(query.Limit).Find(&models).Error; err != nil {
		return nil, err
	}
	messages := make([]domain.Message, len(models))
	for i, model := range models {
		messages[i] = model.toDomain()
	}
	return messages, nil
}

func (r *MessageRepository) SaveEdit(ctx context.Context, edit domain.Edit) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var known int64
		err := tx.Model(&revisionModel{}).
			Where("room_id = ? AND revision_id = ?", edit.RoomID, edit.EventID).Count(&known).Error
		if err != nil || known > 0 {
			return err
		}

		var original messageModel
		err = tx.Where("room_id = ? AND event_id = ?", edit.RoomID, edit.TargetEventID).Take(&original).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return domain.ErrMessageNotFound
		}
		if err != nil {
			return err
		}

		if err := storeOriginalRevision(tx, original); err != nil {
			return err
		}
		revision := revisionModel{
			RoomID: edit.RoomID, EventID: edit.TargetEventID, RevisionID: edit.EventID, Body: edit.Body, CreatedAt: edit.At,
		}
		if err := tx.Create(&revision).Error; err != nil {
			return err
		}

		// The newest edit wins, whatever order the edits arrive in.
		return tx.Model(&messageModel{}).
			Where("seq = ? AND (edited_at IS NULL OR edited_at <= ?)", original.Seq, edit.At).
			Updates(map[string]any{"body": edit.Body, "edited_at": edit.At}).Error
	})
}

// storeOriginalRevision saves the text a message had before its first edit.
func storeOriginalRevision(tx *gorm.DB, original messageModel) error {
	revision := revisionModel{
		RoomID: original.RoomID, EventID: original.EventID, RevisionID: original.EventID,
		Body: original.Body, CreatedAt: original.SentAt,
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&revision).Error
}

func (r *MessageRepository) Revisions(ctx context.Context, roomID, eventID string) ([]domain.Revision, error) {
	var models []revisionModel
	err := r.db.WithContext(ctx).
		Where("room_id = ? AND event_id = ?", roomID, eventID).
		Order("created_at ASC, seq ASC").Find(&models).Error
	if err != nil {
		return nil, err
	}
	revisions := make([]domain.Revision, len(models))
	for i, model := range models {
		revisions[i] = model.toDomain()
	}
	return revisions, nil
}

func (r *MessageRepository) MarkDeleted(ctx context.Context, roomID, eventID string, deletion domain.Deletion, wipeContent bool) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		changes := map[string]any{"deleted_at": deletion.At, "deleted_by": deletion.By, "delete_reason": deletion.Reason}
		if wipeContent {
			changes["body"], changes["media_url"] = "", ""
		}
		result := tx.Model(&messageModel{}).
			Where("room_id = ? AND event_id = ? AND deleted_at IS NULL", roomID, eventID).
			Updates(changes)
		if result.Error != nil || result.RowsAffected == 0 || !wipeContent {
			return result.Error
		}
		return tx.Where("room_id = ? AND event_id = ?", roomID, eventID).Delete(&revisionModel{}).Error
	})
}

func (r *MessageRepository) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	var removed int64
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		expired := tx.Model(&messageModel{}).Select("event_id").Where("sent_at < ?", cutoff)
		err := tx.Where("event_id IN (?)", expired).Delete(&revisionModel{}).Error
		if err != nil {
			return err
		}
		result := tx.Where("sent_at < ?", cutoff).Delete(&messageModel{})
		removed = result.RowsAffected
		return result.Error
	})
	return removed, err
}

func (r *MessageRepository) findOne(tx *gorm.DB) (domain.Message, error) {
	var model messageModel
	err := tx.Take(&model).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.Message{}, domain.ErrMessageNotFound
	}
	if err != nil {
		return domain.Message{}, err
	}
	return model.toDomain(), nil
}
