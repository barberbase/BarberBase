package repository

import (
	"context"

	"github.com/jackc/pgx/v5"
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
