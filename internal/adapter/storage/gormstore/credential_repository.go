package gormstore

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/greatgoodtiver/matrixrss/internal/domain"
	"github.com/greatgoodtiver/matrixrss/internal/usecase"
)

var (
	_ usecase.APIKeyRepository    = (*APIKeyRepository)(nil)
	_ usecase.FeedTokenRepository = (*FeedTokenRepository)(nil)
)

type APIKeyRepository struct {
	db *gorm.DB
}

func (r *APIKeyRepository) Create(ctx context.Context, key *domain.APIKey, secretHash string) error {
	model := apiKeyModel{
		Name: key.Name, Hash: secretHash, Hint: key.Hint, Rooms: key.Rooms, ExpiresAt: key.ExpiresAt,
		CreatedAt: key.CreatedAt, CreatedBy: key.CreatedBy,
	}
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return err
	}
	key.ID = model.ID
	return nil
}

func (r *APIKeyRepository) FindByHash(ctx context.Context, secretHash string) (domain.APIKey, error) {
	var model apiKeyModel
	err := r.db.WithContext(ctx).Where("hash = ?", secretHash).Take(&model).Error
	if err != nil {
		return domain.APIKey{}, credentialError(err)
	}
	return model.toDomain(), nil
}

func (r *APIKeyRepository) Get(ctx context.Context, id uint64) (domain.APIKey, error) {
	var model apiKeyModel
	if err := r.db.WithContext(ctx).Where("id = ?", id).Take(&model).Error; err != nil {
		return domain.APIKey{}, credentialError(err)
	}
	return model.toDomain(), nil
}

func (r *APIKeyRepository) List(ctx context.Context) ([]domain.APIKey, error) {
	var models []apiKeyModel
	if err := r.db.WithContext(ctx).Order("id").Find(&models).Error; err != nil {
		return nil, err
	}
	keys := make([]domain.APIKey, len(models))
	for i, model := range models {
		keys[i] = model.toDomain()
	}
	return keys, nil
}

func (r *APIKeyRepository) Revoke(ctx context.Context, id uint64, at time.Time) error {
	return revoke(ctx, r.db, &apiKeyModel{}, id, at)
}

type FeedTokenRepository struct {
	db *gorm.DB
}

func (r *FeedTokenRepository) Create(ctx context.Context, token *domain.FeedToken, secretHash string) error {
	model := feedTokenModel{
		RoomID: token.RoomID, Name: token.Name, Hash: secretHash, Hint: token.Hint,
		CreatedAt: token.CreatedAt, CreatedBy: token.CreatedBy,
	}
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return err
	}
	token.ID = model.ID
	return nil
}

func (r *FeedTokenRepository) FindByHash(ctx context.Context, secretHash string) (domain.FeedToken, error) {
	var model feedTokenModel
	err := r.db.WithContext(ctx).Where("hash = ?", secretHash).Take(&model).Error
	if err != nil {
		return domain.FeedToken{}, credentialError(err)
	}
	return model.toDomain(), nil
}

func (r *FeedTokenRepository) Get(ctx context.Context, id uint64) (domain.FeedToken, error) {
	var model feedTokenModel
	if err := r.db.WithContext(ctx).Where("id = ?", id).Take(&model).Error; err != nil {
		return domain.FeedToken{}, credentialError(err)
	}
	return model.toDomain(), nil
}

func (r *FeedTokenRepository) ListByRoom(ctx context.Context, roomID string) ([]domain.FeedToken, error) {
	var models []feedTokenModel
	if err := r.db.WithContext(ctx).Where("room_id = ?", roomID).Order("id").Find(&models).Error; err != nil {
		return nil, err
	}
	tokens := make([]domain.FeedToken, len(models))
	for i, model := range models {
		tokens[i] = model.toDomain()
	}
	return tokens, nil
}

func (r *FeedTokenRepository) Revoke(ctx context.Context, id uint64, at time.Time) error {
	return revoke(ctx, r.db, &feedTokenModel{}, id, at)
}

func (r *FeedTokenRepository) RevokeAllForRoom(ctx context.Context, roomID string, at time.Time) error {
	return r.db.WithContext(ctx).Model(&feedTokenModel{}).
		Where("room_id = ? AND revoked_at IS NULL", roomID).
		Update("revoked_at", at).Error
}

// revoke marks the row as revoked unless it already is, and reports unknown IDs.
func revoke(ctx context.Context, db *gorm.DB, model any, id uint64, at time.Time) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var known int64
		if err := tx.Model(model).Where("id = ?", id).Count(&known).Error; err != nil {
			return err
		}
		if known == 0 {
			return domain.ErrCredentialNotFound
		}
		return tx.Model(model).Where("id = ? AND revoked_at IS NULL", id).Update("revoked_at", at).Error
	})
}

func credentialError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.ErrCredentialNotFound
	}
	return err
}
