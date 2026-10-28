package gormstore

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/greatgoodtiver/matrixrss/internal/domain"
	"github.com/greatgoodtiver/matrixrss/internal/usecase"
)

var _ usecase.FeedRepository = (*FeedRepository)(nil)

type FeedRepository struct {
	db *gorm.DB
}

func (r *FeedRepository) Save(ctx context.Context, feed domain.Feed) error {
	model := newFeedModel(feed)
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{UpdateAll: true}).
		Create(&model).Error
}

func (r *FeedRepository) FindByRoom(ctx context.Context, roomID string) (domain.Feed, error) {
	var model feedModel
	err := r.db.WithContext(ctx).Where("room_id = ?", roomID).Take(&model).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.Feed{}, domain.ErrFeedNotFound
	}
	if err != nil {
		return domain.Feed{}, err
	}
	return model.toDomain(), nil
}

func (r *FeedRepository) ListEnabled(ctx context.Context) ([]domain.Feed, error) {
	var models []feedModel
	err := r.db.WithContext(ctx).Where("enabled = ?", true).Order("created_at").Find(&models).Error
	if err != nil {
		return nil, err
	}
	feeds := make([]domain.Feed, len(models))
	for i, model := range models {
		feeds[i] = model.toDomain()
	}
	return feeds, nil
}
