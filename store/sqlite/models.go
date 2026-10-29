package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/model"
)

// Models shares the owning Devices connection and lifetime. Since ModelStore has
// no error return, database failures panic rather than reporting false success.
type Models struct{ db *sql.DB }

var _ apiv1.ModelStore = (*Models)(nil)

func (s *Devices) Models() *Models { return &Models{db: s.db} }

func (s *Models) List(tenant string) []*model.Object {
	rows, err := s.db.Query("SELECT payload FROM models WHERE tenant=?", tenant)
	if err != nil {
		panic(fmt.Errorf("models list: %w", err))
	}
	defer rows.Close()
	var result []*model.Object
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			panic(fmt.Errorf("models scan: %w", err))
		}
		var obj model.Object
		if err := json.Unmarshal([]byte(payload), &obj); err != nil {
			panic(fmt.Errorf("models decode: %w", err))
		}
		result = append(result, &obj)
	}
	if err := rows.Err(); err != nil {
		panic(fmt.Errorf("models list: %w", err))
	}
	return result
}
func (s *Models) Put(tenant string, object *model.Object) {
	payload, err := json.Marshal(object)
	if err != nil {
		panic(fmt.Errorf("models encode: %w", err))
	}
	_, err = s.db.Exec(`INSERT INTO models(tenant,object_id,major,minor,payload) VALUES(?,?,?,?,?)
 ON CONFLICT(tenant,object_id,major,minor) DO UPDATE SET payload=excluded.payload`, tenant, object.ID, object.Version.Major, object.Version.Minor, string(payload))
	if err != nil {
		panic(fmt.Errorf("models put: %w", err))
	}
}
func (s *Models) Delete(tenant string, id uint16, version model.Version) bool {
	result, err := s.db.Exec("DELETE FROM models WHERE tenant=? AND object_id=? AND major=? AND minor=?", tenant, id, version.Major, version.Minor)
	if err != nil {
		panic(fmt.Errorf("models delete: %w", err))
	}
	count, err := result.RowsAffected()
	if err != nil {
		panic(fmt.Errorf("models delete: %w", err))
	}
	return count != 0
}
