package switchmod

import (
	"net/http"
	"strconv"

	"itmsapiproject/internal/core"
)

// HandleModel 处理 /itms/handleSwitchModel/（型号：0 查询、1 新增、2 修改、3 删除）。
func (m *Module) HandleModel(w http.ResponseWriter, r *http.Request) {
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
		models, err := m.store.GetAllModels()
		if err != nil {
			core.WriteAPI(w, http.StatusInternalServerError, core.CodeFail, "failed to list models: "+err.Error(), nil)
			return
		}
		core.WriteAPI(w, http.StatusOK, core.CodeOK, "success", models)
	case 1, 2:
		var model SwitchModel
		if err := decodeSaveData(r, &model); err != nil {
			core.WriteAPI(w, http.StatusOK, core.CodeFail, "invalid request: "+err.Error(), nil)
			return
		}
		if act == 1 {
			id, err := m.store.CreateModel(model)
			if err != nil {
				core.WriteAPI(w, http.StatusInternalServerError, core.CodeFail, "failed to create model: "+err.Error(), nil)
				return
			}
			core.WriteAPI(w, http.StatusOK, core.CodeOK, "success", id)
			return
		}
		affected, err := m.store.UpdateModel(model.Id, model.Name, model.VenueId, model.PurposeId,
			model.ProtocolId, model.EthPort, model.Remarks, model.ManufacturerId)
		if err != nil {
			core.WriteAPI(w, http.StatusInternalServerError, core.CodeFail, "failed to update model: "+err.Error(), nil)
			return
		}
		core.WriteAPI(w, http.StatusOK, core.CodeOK, "success", affected)
	case 3:
		var ids []int
		if err := decodeSaveData(r, &ids); err != nil {
			core.WriteAPI(w, http.StatusOK, core.CodeFail, "invalid request: "+err.Error(), nil)
			return
		}
		affected, err := m.store.DeleteModel(ids)
		if err != nil {
			core.WriteAPI(w, http.StatusInternalServerError, core.CodeFail, "failed to delete models: "+err.Error(), nil)
			return
		}
		core.WriteAPI(w, http.StatusOK, core.CodeOK, "success", affected)
	default:
		core.WriteAPI(w, http.StatusOK, core.CodeFail, "invalid nAct", nil)
	}
}

// HandleDeviceLedger 处理 /itms/handleSwitchInfo/（设备台账：0 查询、1 新增、2 修改、3 删除）。
//
// 与老实现一致的两点：
//   - nRpcType 为 0 时按默认值 3（SSH）落库；
//   - 设备账号密码以 AES-GCM 密文入库、读取时解密。
func (m *Module) HandleDeviceLedger(w http.ResponseWriter, r *http.Request) {
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
		devices, err := m.store.GetAllDeviceInformation()
		if err != nil {
			core.WriteAPI(w, http.StatusInternalServerError, core.CodeFail, "failed to list device information: "+err.Error(), nil)
			return
		}
		core.WriteAPI(w, http.StatusOK, core.CodeOK, "success", devices)
	case 1, 2:
		var device SwitchDeviceInformation
		if err := decodeSaveData(r, &device); err != nil {
			core.WriteAPI(w, http.StatusOK, core.CodeFail, "invalid request: "+err.Error(), nil)
			return
		}
		if device.RpcType == 0 {
			device.RpcType = 3
		}
		if act == 1 {
			id, err := m.store.CreateDeviceInformation(device)
			if err != nil {
				core.WriteAPI(w, http.StatusInternalServerError, core.CodeFail, "failed to create device information: "+err.Error(), nil)
				return
			}
			core.WriteAPI(w, http.StatusOK, core.CodeOK, "success", id)
			return
		}
		affected, err := m.store.UpdateDeviceInformation(device.Id, device.Name, device.DevMac, device.ManageIP,
			device.Gateway, device.SubnetMask, device.ServerIP, device.DevPassword, device.ModelId,
			device.PurposeId, device.AutoEnable, device.Remarks, device.RpcHostip, device.RpcPort,
			device.RpcUsername, device.RpcPassword, device.RpcType)
		if err != nil {
			core.WriteAPI(w, http.StatusInternalServerError, core.CodeFail, "failed to update device information: "+err.Error(), nil)
			return
		}
		core.WriteAPI(w, http.StatusOK, core.CodeOK, "success", affected)
	case 3:
		var ids []int
		if err := decodeSaveData(r, &ids); err != nil {
			core.WriteAPI(w, http.StatusOK, core.CodeFail, "invalid request: "+err.Error(), nil)
			return
		}
		affected, err := m.store.DeleteDeviceInformation(ids)
		if err != nil {
			core.WriteAPI(w, http.StatusInternalServerError, core.CodeFail, "failed to delete device information: "+err.Error(), nil)
			return
		}
		core.WriteAPI(w, http.StatusOK, core.CodeOK, "success", affected)
	default:
		core.WriteAPI(w, http.StatusOK, core.CodeFail, "invalid nAct", nil)
	}
}
