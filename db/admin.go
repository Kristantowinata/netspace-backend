package db

import (
	"context"
	"time"

	"github.com/latoiste/netspace/api"
	"github.com/latoiste/netspace/model"
)

func (r *Repository) AdminByUsername(username string, ctx context.Context) (*model.Admin, error) {
	const query = `
		SELECT 
			a."id",
			a.username,
			a."password",
			a."role",
			a."plan",
			a."avatar",
			a."name",
			a.locationid,
			l.slug
		FROM admins a
		JOIN locations l ON l.id = a.locationid
		WHERE a.username = $1
	`

	row := r.db.QueryRowContext(
		ctx,
		query,
		username,
	)

	var admin model.Admin

	if err := row.Scan(
		&admin.Id,
		&admin.Username,
		&admin.Password,
		&admin.Role,
		&admin.Plan,
		&admin.Avatar,
		&admin.Name,
		&admin.LocationId,
		&admin.LocationSlug,
	); err != nil {
		return nil, err
	}

	return &admin, nil
}

func (r *Repository) AdminById(adminId string, ctx context.Context) (*model.Admin, error) {
	const query = `
		SELECT
			a."id",
			a.username,
			a."password",
			a."role",
			a."plan",
			a."avatar",
			a."name",
			a.locationid,
			l.slug
		FROM admins a
		JOIN locations l ON l.id = a.locationid
		WHERE a.id = $1
	`

	row := r.db.QueryRowContext(ctx, query, adminId)
	var admin model.Admin
	if err := row.Scan(
		&admin.Id,
		&admin.Username,
		&admin.Password,
		&admin.Role,
		&admin.Plan,
		&admin.Avatar,
		&admin.Name,
		&admin.LocationId,
		&admin.LocationSlug,
	); err != nil {
		return nil, err
	}
	return &admin, nil
}

func (r *Repository) UpdateAdminPassword(adminId string, passwordHash string, ctx context.Context) error {
	const query = `
		UPDATE admins
		SET "password" = $1
		WHERE id = $2
	`

	_, err := r.db.ExecContext(ctx, query, passwordHash, adminId)
	return err
}

func (r *Repository) TotalCheckInRange(start time.Time, end time.Time, ctx context.Context) (int, error) {
	const query = `
		SELECT COUNT(*)
		FROM users
		WHERE createdAt >= $1 AND createdAt < $2
	`

	row := r.db.QueryRowContext(
		ctx,
		query,
		start,
		end,
	)

	var count int

	if err := row.Scan(
		&count,
	); err != nil {
		return 0, err
	}

	return count, nil
}

func (r *Repository) TotalActiveUsersRange(start time.Time, end time.Time, ctx context.Context) (int, error) {
	const query = `
		SELECT COUNT(*)
		FROM users
		WHERE createdAt >= $1 AND createdAt < $2 AND isactive = true
	`

	row := r.db.QueryRowContext(
		ctx,
		query,
		start,
		end,
	)

	var count int

	if err := row.Scan(
		&count,
	); err != nil {
		return 0, err
	}

	return count, nil
}

func (r *Repository) TotalMessagesRange(start time.Time, end time.Time, ctx context.Context) (int, error) {
	const query = `
		SELECT
		(
			SELECT COUNT(*)
			FROM PrivateMessages
			WHERE timestamp >= $1 AND timestamp < $2
		)
		+
		(
			SELECT COUNT(*)
			FROM PublicMessages
			WHERE timestamp >= $1 AND timestamp < $2
		)
		+
		(
			SELECT COUNT(*)
			FROM GroupMessages
			WHERE timestamp >= $1 AND timestamp < $2
		)
    `

	row := r.db.QueryRowContext(
		ctx,
		query,
		start,
		end,
	)

	var count int

	if err := row.Scan(
		&count,
	); err != nil {
		return 0, err
	}

	return count, nil
}

func (r *Repository) ActiveUsers(locationId int, ctx context.Context) (int, error) {
	const query = `
		SELECT COUNT(*)
		FROM users
		WHERE isactive = true AND locationid = $1
	`

	row := r.db.QueryRowContext(ctx, query, locationId)

	var count int

	if err := row.Scan(
		&count,
	); err != nil {
		return 0, err
	}

	return count, nil
}

func (r *Repository) GetTopInterests(ctx context.Context) ([]api.InterestPercentageDTO, error) {
	const query = `
		SELECT
			i.emoji,
			i.label,
			ROUND(
				COUNT(*) * 100.0 / SUM(COUNT(*)) OVER (),
				0
			) AS percentage
		FROM UserInterests ui
		JOIN Interests i
			ON i.id = ui.interestId
		GROUP BY i.emoji, i.label
		ORDER BY percentage DESC;
	`

	topInterests := make([]api.InterestPercentageDTO, 0)

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var it api.InterestPercentageDTO
		err := rows.Scan(
			&it.Emoji,
			&it.Label,
			&it.Percentage,
		)
		if err != nil {
			return nil, err
		}
		topInterests = append(topInterests, it)
	}

	return topInterests, nil
}
