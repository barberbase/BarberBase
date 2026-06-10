package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// VariantRow is the DB projection used by the booking resolver.
type VariantRow struct {
	ID                  string
	DurationMinutes     int
	PricePaise          int
	AllowWalkIn         bool
	AllowAppointment    bool
	RequiresAppointment bool
}

// ServiceVariantDB is the DB result row for a service variant (full).
type ServiceVariantDB struct {
	ID                  string
	Name                string
	Description         *string
	DurationMinutes     int
	PricePaise          int
	AllowWalkIn         bool
	AllowAppointment    bool
	RequiresAppointment bool
	IsPopular           bool
	SortOrder           int
}

// ServiceGroupDB groups a set of variants under a named group.
type ServiceGroupDB struct {
	ID          string
	Name        string
	Description *string
	SortOrder   int
	Variants    []ServiceVariantDB
}

// ServiceCategoryDB is the top-level catalog node.
type ServiceCategoryDB struct {
	ID        string
	Name      string
	Gender    string
	SortOrder int
	Groups    []ServiceGroupDB
}

// ServiceVariantWithContext includes group + category name for search results.
type ServiceVariantWithContext struct {
	ServiceVariantDB
	GroupName    string
	CategoryName string
}

// GetServiceCatalog returns the full hierarchy filtered by gender and/or category_id.
// gender: "all" | "men" | "women" | "unisex". Pass "all" to skip filter.
// categoryID: empty string to skip filter; UUID string to filter to one category.
func GetServiceCatalog(ctx context.Context, pool *pgxpool.Pool, locationID, gender, categoryID string) ([]ServiceCategoryDB, error) {
	query := `
		SELECT
			sc.id  AS cat_id,  sc.name  AS cat_name, sc.gender, sc.sort_order AS cat_sort,
			sg.id  AS grp_id,  sg.name  AS grp_name, sg.description AS grp_desc,
			sg.sort_order AS grp_sort,
			sv.id, sv.name, sv.description, sv.duration_minutes, sv.price_paise,
			sv.allow_walk_in, sv.allow_appointment, sv.requires_appointment,
			sv.is_popular, sv.sort_order AS var_sort
		FROM service_categories sc
		JOIN service_groups sg   ON sg.category_id  = sc.id AND sg.is_active = true
		JOIN service_variants sv ON sv.group_id      = sg.id AND sv.is_active = true
		WHERE sc.location_id = $1::UUID AND sc.is_active = true
		  AND ($2 = 'all' OR sc.gender = $2)
		  AND ($3 = '' OR sc.id = NULLIF($3, '')::UUID)
		ORDER BY sc.sort_order, sg.sort_order, sv.sort_order
	`
	rows, err := pool.Query(ctx, query, locationID, gender, categoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type rowData struct {
		catID, catName, catGender string
		catSort int
		grpID, grpName string
		grpDesc *string
		grpSort int
		v ServiceVariantDB
	}
	var data []rowData
	for rows.Next() {
		var d rowData
		err := rows.Scan(
			&d.catID, &d.catName, &d.catGender, &d.catSort,
			&d.grpID, &d.grpName, &d.grpDesc, &d.grpSort,
			&d.v.ID, &d.v.Name, &d.v.Description, &d.v.DurationMinutes, &d.v.PricePaise,
			&d.v.AllowWalkIn, &d.v.AllowAppointment, &d.v.RequiresAppointment,
			&d.v.IsPopular, &d.v.SortOrder,
		)
		if err != nil {
			return nil, err
		}
		data = append(data, d)
	}

	var categories []ServiceCategoryDB
	var currentCat *ServiceCategoryDB
	var currentGrp *ServiceGroupDB

	for _, d := range data {
		if currentCat == nil || currentCat.ID != d.catID {
			if currentCat != nil {
				if currentGrp != nil {
					currentCat.Groups = append(currentCat.Groups, *currentGrp)
					currentGrp = nil
				}
				categories = append(categories, *currentCat)
			}
			currentCat = &ServiceCategoryDB{
				ID:        d.catID,
				Name:      d.catName,
				Gender:    d.catGender,
				SortOrder: d.catSort,
				Groups:    []ServiceGroupDB{},
			}
		}
		if currentGrp == nil || currentGrp.ID != d.grpID {
			if currentGrp != nil {
				currentCat.Groups = append(currentCat.Groups, *currentGrp)
			}
			currentGrp = &ServiceGroupDB{
				ID:          d.grpID,
				Name:        d.grpName,
				Description: d.grpDesc,
				SortOrder:   d.grpSort,
				Variants:    []ServiceVariantDB{},
			}
		}
		currentGrp.Variants = append(currentGrp.Variants, d.v)
	}
	if currentCat != nil {
		if currentGrp != nil {
			currentCat.Groups = append(currentCat.Groups, *currentGrp)
		}
		categories = append(categories, *currentCat)
	}

	if categories == nil {
		categories = []ServiceCategoryDB{}
	}
	return categories, nil
}

// SearchServiceVariants performs case-insensitive partial match on variant + group names.
// q must be 2..100 chars (validated by handler).
func SearchServiceVariants(ctx context.Context, pool *pgxpool.Pool, locationID, q string) ([]ServiceVariantWithContext, error) {
	query := `
		SELECT sv.id, sv.name, sv.description, sv.duration_minutes, sv.price_paise,
		       sv.allow_walk_in, sv.allow_appointment, sv.requires_appointment,
		       sv.is_popular, sv.sort_order,
		       sg.name AS group_name, sc.name AS category_name
		FROM service_variants sv
		JOIN service_groups     sg ON sg.id  = sv.group_id    AND sg.is_active = true
		JOIN service_categories sc ON sc.id  = sg.category_id AND sc.is_active = true
		WHERE sv.location_id = $1::UUID AND sv.is_active = true
		  AND (sv.name ILIKE '%' || $2 || '%' OR sg.name ILIKE '%' || $2 || '%')
		ORDER BY sv.is_popular DESC, sv.sort_order, sv.name
		LIMIT 20
	`
	rows, err := pool.Query(ctx, query, locationID, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []ServiceVariantWithContext
	for rows.Next() {
		var v ServiceVariantWithContext
		err := rows.Scan(
			&v.ID, &v.Name, &v.Description, &v.DurationMinutes, &v.PricePaise,
			&v.AllowWalkIn, &v.AllowAppointment, &v.RequiresAppointment,
			&v.IsPopular, &v.SortOrder,
			&v.GroupName, &v.CategoryName,
		)
		if err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	if result == nil {
		result = []ServiceVariantWithContext{}
	}
	return result, nil
}

// GetVariantsByIDs batch-loads variants and validates they all belong to locationID.
// Returns error if any variant is not found or does not belong to the location.
// Used by resolveBookingOptions and createCheckinIntent.
func GetVariantsByIDs(ctx context.Context, pool *pgxpool.Pool, locationID string, variantIDs []string) ([]VariantRow, error) {
	if len(variantIDs) == 0 {
		return []VariantRow{}, nil
	}
	query := `
		SELECT id, duration_minutes, price_paise,
		       allow_walk_in, allow_appointment, requires_appointment
		FROM service_variants
		WHERE id = ANY($1::UUID[])
		  AND location_id = $2::UUID
		  AND is_active = true
	`
	rows, err := pool.Query(ctx, query, variantIDs, locationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var variants []VariantRow
	for rows.Next() {
		var v VariantRow
		err := rows.Scan(
			&v.ID, &v.DurationMinutes, &v.PricePaise,
			&v.AllowWalkIn, &v.AllowAppointment, &v.RequiresAppointment,
		)
		if err != nil {
			return nil, err
		}
		variants = append(variants, v)
	}

	if len(variants) != len(variantIDs) {
		return nil, pgx.ErrNoRows // handler will return 422
	}
	return variants, nil
}

// ErrVariantExists is returned when a variant name already exists in this group.
var ErrVariantExists = errors.New("variant name already exists in this group")

type ServiceVariantRow struct {
	ID                  string
	Name                string
	Description         *string
	DurationMinutes     int
	PricePaise          int
	AllowWalkIn         bool
	AllowAppointment    bool
	RequiresAppointment bool
	IsPopular           bool
	IsActive            bool
}

type ServiceGroupRow struct {
	ID          string
	Name        string
	Description *string
	Variants    []ServiceVariantRow
}

type ServiceCategoryRow struct {
	ID        string
	Name      string
	Gender    string
	SortOrder int
	Groups    []ServiceGroupRow
}

type CreateServiceVariantParams struct {
	CategoryName        string
	CategoryGender      string
	GroupName           string
	VariantName         string
	DurationMinutes     int
	PricePaise          int
	AllowWalkIn         bool
	AllowAppointment    bool
	RequiresAppointment bool
	IsPopular           bool
}

type UpdateServiceVariantParams struct {
	VariantName     *string
	DurationMinutes *int
	PricePaise      *int
	IsActive        *bool
	IsPopular       *bool
}

type ServiceRepository struct {
	Pool *pgxpool.Pool
}

func (r *ServiceRepository) ListServicesForAdmin(ctx context.Context, tenantID, locationID string) ([]ServiceCategoryRow, error) {
	query := `
		SELECT
			sc.id         AS cat_id,
			sc.name       AS cat_name,
			sc.gender     AS cat_gender,
			sc.sort_order AS cat_sort,
			sg.id         AS grp_id,
			sg.name       AS grp_name,
			sg.description AS grp_desc,
			sg.sort_order  AS grp_sort,
			sv.id          AS var_id,
			sv.name        AS var_name,
			sv.description AS var_desc,
			sv.duration_minutes,
			sv.price_paise,
			sv.allow_walk_in,
			sv.allow_appointment,
			sv.requires_appointment,
			sv.is_popular,
			sv.is_active
		FROM service_categories sc
		JOIN service_groups sg
			ON sg.category_id = sc.id
		   AND sg.tenant_id   = $1::UUID
		   AND sg.is_active   = true
		JOIN service_variants sv
			ON sv.group_id   = sg.id
		   AND sv.tenant_id  = $1::UUID
		   AND sv.is_active  = true
		WHERE sc.location_id = $2::UUID
		  AND sc.tenant_id   = $1::UUID
		  AND sc.is_active   = true
		ORDER BY sc.sort_order, sc.name, sg.sort_order, sg.name, sv.sort_order, sv.name
	`

	rows, err := r.Pool.Query(ctx, query, tenantID, locationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var categories []ServiceCategoryRow
	var catIndex = make(map[string]int)
	var grpIndex = make(map[string]int)

	for rows.Next() {
		var catID, catName, catGender string
		var catSort, grpSort int
		var grpID, grpName string
		var grpDesc *string
		var v ServiceVariantRow

		err := rows.Scan(
			&catID, &catName, &catGender, &catSort,
			&grpID, &grpName, &grpDesc, &grpSort,
			&v.ID, &v.Name, &v.Description, &v.DurationMinutes, &v.PricePaise,
			&v.AllowWalkIn, &v.AllowAppointment, &v.RequiresAppointment, &v.IsPopular, &v.IsActive,
		)
		if err != nil {
			return nil, err
		}

		cIdx, exists := catIndex[catID]
		if !exists {
			cIdx = len(categories)
			categories = append(categories, ServiceCategoryRow{
				ID:        catID,
				Name:      catName,
				Gender:    catGender,
				SortOrder: catSort,
				Groups:    []ServiceGroupRow{},
			})
			catIndex[catID] = cIdx
		}

		gKey := catID + ":" + grpID
		gIdx, exists := grpIndex[gKey]
		if !exists {
			gIdx = len(categories[cIdx].Groups)
			categories[cIdx].Groups = append(categories[cIdx].Groups, ServiceGroupRow{
				ID:          grpID,
				Name:        grpName,
				Description: grpDesc,
				Variants:    []ServiceVariantRow{},
			})
			grpIndex[gKey] = gIdx
		}

		categories[cIdx].Groups[gIdx].Variants = append(categories[cIdx].Groups[gIdx].Variants, v)
	}

	if categories == nil {
		categories = []ServiceCategoryRow{}
	}

	return categories, nil
}

func (r *ServiceRepository) CreateServiceVariant(ctx context.Context, tenantID, locationID string, p CreateServiceVariantParams) (ServiceVariantRow, error) {
	var variant ServiceVariantRow

	err := WithTx(ctx, r.Pool, func(tx pgx.Tx) error {
		// Step 1: Upsert category
		var categoryID string
		err := tx.QueryRow(ctx, `
			INSERT INTO service_categories (id, tenant_id, location_id, name, gender)
			VALUES (gen_random_uuid(), $1::UUID, $2::UUID, $3, $4)
			ON CONFLICT (location_id, name, gender)
			DO UPDATE SET is_active = true
			RETURNING id
		`, tenantID, locationID, p.CategoryName, p.CategoryGender).Scan(&categoryID)
		if err != nil {
			return err
		}

		// Step 2: Upsert group
		var groupID string
		err = tx.QueryRow(ctx, `
			INSERT INTO service_groups (id, tenant_id, location_id, category_id, name)
			VALUES (gen_random_uuid(), $1::UUID, $2::UUID, $3::UUID, $4)
			ON CONFLICT (location_id, category_id, name)
			DO UPDATE SET is_active = true
			RETURNING id
		`, tenantID, locationID, categoryID, p.GroupName).Scan(&groupID)
		if err != nil {
			return err
		}

		// Step 3: Insert variant
		err = tx.QueryRow(ctx, `
			INSERT INTO service_variants (
				id, tenant_id, location_id, group_id,
				name, duration_minutes, price_paise,
				allow_walk_in, allow_appointment, requires_appointment, is_popular, is_active
			) VALUES (
				gen_random_uuid(), $1::UUID, $2::UUID, $3::UUID,
				$4, $5, $6,
				$7, $8, $9, $10, true
			)
			RETURNING id, name, description, duration_minutes, price_paise,
			          allow_walk_in, allow_appointment, requires_appointment, is_popular, is_active
		`, tenantID, locationID, groupID, p.VariantName, p.DurationMinutes, p.PricePaise,
			p.AllowWalkIn, p.AllowAppointment, p.RequiresAppointment, p.IsPopular).Scan(
			&variant.ID, &variant.Name, &variant.Description, &variant.DurationMinutes, &variant.PricePaise,
			&variant.AllowWalkIn, &variant.AllowAppointment, &variant.RequiresAppointment, &variant.IsPopular, &variant.IsActive,
		)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return ErrVariantExists
			}
			return err
		}

		return nil
	})

	if err != nil {
		return ServiceVariantRow{}, err
	}

	return variant, nil
}

func (r *ServiceRepository) UpdateServiceVariant(ctx context.Context, tenantID, locationID, variantID string, p UpdateServiceVariantParams) (ServiceVariantRow, error) {
	var variant ServiceVariantRow

	query := `
		UPDATE service_variants
		SET
			name             = COALESCE($3, name),
			duration_minutes = COALESCE($4, duration_minutes),
			price_paise      = COALESCE($5, price_paise),
			is_active        = COALESCE($6, is_active),
			is_popular       = COALESCE($7, is_popular),
			updated_at       = NOW()
		WHERE id          = $2::UUID
		  AND tenant_id   = $1::UUID
		  AND location_id = $8::UUID
		RETURNING id, name, description, duration_minutes, price_paise,
		          allow_walk_in, allow_appointment, requires_appointment, is_popular, is_active
	`

	err := r.Pool.QueryRow(ctx, query,
		tenantID, variantID, p.VariantName, p.DurationMinutes, p.PricePaise,
		p.IsActive, p.IsPopular, locationID,
	).Scan(
		&variant.ID, &variant.Name, &variant.Description, &variant.DurationMinutes, &variant.PricePaise,
		&variant.AllowWalkIn, &variant.AllowAppointment, &variant.RequiresAppointment, &variant.IsPopular, &variant.IsActive,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ServiceVariantRow{}, pgx.ErrNoRows
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ServiceVariantRow{}, ErrVariantExists
		}
		return ServiceVariantRow{}, err
	}

	return variant, nil
}

