package switchmod

import (
	"encoding/json"
	"net/http"
	"strconv"

	"itmsapiproject/internal/core"
)

// 老接口处理器：路径、nAct 语义、字段名与错误消息都与老实现保持一致（黄金用例可直接接管）。
// 所有响应统一走 core.WriteAPI（{code,message,data} + 服务端路径清洗）。

// handleNameOnlyResource 处理「只有 nId/szName 两列」的基础资料接口
// （场景 / 制造商 / 用途 / 协议共用同一套 nAct 语义：0 查询、1 新增、2 修改、3 删除）。
func handleNameOnlyResource[T any](w http.ResponseWriter, r *http.Request, ops nameOnlyOps[T]) {
	if r.Method != http.MethodPost {
		core.WriteMethodNotAllowed(w)
		return
	}
	act, err := strconv.Atoi(r.FormValue("nAct"))
	if err != nil {
		core.WriteAPI(w, http.StatusOK, core.CodeFail, "invalid nAct: "+err.Error(), nil)
		return
	}
	switch act {
	case 0:
		items, err := ops.list()
		if err != nil {
			core.WriteAPI(w, http.StatusInternalServerError, core.CodeFail, "failed to list "+ops.label+": "+err.Error(), nil)
			return
		}
		core.WriteAPI(w, http.StatusOK, core.CodeOK, "success", items)
	case 1, 2:
		var item T
		if err := decodeSaveData(r, &item); err != nil {
			core.WriteAPI(w, http.StatusOK, core.CodeFail, "invalid request: "+err.Error(), nil)
			return
		}
		if act == 1 {
			id, err := ops.create(item)
			if err != nil {
				core.WriteAPI(w, http.StatusInternalServerError, core.CodeFail, "failed to create "+ops.label+": "+err.Error(), nil)
				return
			}
			core.WriteAPI(w, http.StatusOK, core.CodeOK, "success", id)
			return
		}
		affected, err := ops.update(ops.idOf(item), ops.nameOf(item))
		if err != nil {
			core.WriteAPI(w, http.StatusInternalServerError, core.CodeFail, "failed to update "+ops.label+": "+err.Error(), nil)
			return
		}
		core.WriteAPI(w, http.StatusOK, core.CodeOK, "success", affected)
	case 3:
		var ids []int
		if err := decodeSaveData(r, &ids); err != nil {
			core.WriteAPI(w, http.StatusOK, core.CodeFail, "invalid request: "+err.Error(), nil)
			return
		}
		affected, err := ops.delete(ids)
		if err != nil {
			core.WriteAPI(w, http.StatusInternalServerError, core.CodeFail, "failed to delete "+ops.label+"s: "+err.Error(), nil)
			return
		}
		core.WriteAPI(w, http.StatusOK, core.CodeOK, "success", affected)
	default:
		core.WriteAPI(w, http.StatusOK, core.CodeFail, "invalid nAct", nil)
	}
}

// nameOnlyOps 是基础资料四类资源的统一操作集合。
type nameOnlyOps[T any] struct {
	label  string
	create func(T) (int64, error)
	list   func() ([]T, error)
	update func(int64, string) (int64, error)
	delete func([]int) (int64, error)
	idOf   func(T) int64
	nameOf func(T) string
}

// decodeSaveData 解析老前端的 saveData（JSON 字符串，query 与 form 均可）。
func decodeSaveData(r *http.Request, target any) error {
	raw := r.FormValue("saveData")
	return json.Unmarshal([]byte(raw), target)
}

// HandleVenue 处理 /itms/handleSwitchVenue/。
func (m *Module) HandleVenue(w http.ResponseWriter, r *http.Request) {
	handleNameOnlyResource(w, r, nameOnlyOps[SwitchVenue]{
		label:  "venue",
		create: m.store.CreateVenue,
		list:   m.store.GetAllVenues,
		update: m.store.UpdateVenue,
		delete: m.store.DeleteVenue,
		idOf:   func(item SwitchVenue) int64 { return item.Id },
		nameOf: func(item SwitchVenue) string { return item.Name },
	})
}

// HandleManufacturer 处理 /itms/handleSwitchManufacturer/。
func (m *Module) HandleManufacturer(w http.ResponseWriter, r *http.Request) {
	handleNameOnlyResource(w, r, nameOnlyOps[SwitchManufacturer]{
		label:  "manufacturer",
		create: m.store.CreateManufacturer,
		list:   m.store.GetAllManufacturers,
		update: m.store.UpdateManufacturer,
		delete: m.store.DeleteManufacturer,
		idOf:   func(item SwitchManufacturer) int64 { return item.Id },
		nameOf: func(item SwitchManufacturer) string { return item.Name },
	})
}

// HandlePurpose 处理 /itms/handleSwitchPurpose/。
func (m *Module) HandlePurpose(w http.ResponseWriter, r *http.Request) {
	handleNameOnlyResource(w, r, nameOnlyOps[SwitchPurpose]{
		label:  "purpose",
		create: m.store.CreatePurpose,
		list:   m.store.GetAllPurposes,
		update: m.store.UpdatePurpose,
		delete: m.store.DeletePurpose,
		idOf:   func(item SwitchPurpose) int64 { return item.Id },
		nameOf: func(item SwitchPurpose) string { return item.Name },
	})
}

// HandleProtocol 处理 /itms/handleSwitchProtocol/。
func (m *Module) HandleProtocol(w http.ResponseWriter, r *http.Request) {
	handleNameOnlyResource(w, r, nameOnlyOps[SwitchProtocol]{
		label:  "protocol",
		create: m.store.CreateProtocol,
		list:   m.store.GetAllProtocols,
		update: m.store.UpdateProtocol,
		delete: m.store.DeleteProtocol,
		idOf:   func(item SwitchProtocol) int64 { return item.Id },
		nameOf: func(item SwitchProtocol) string { return item.Name },
	})
}
