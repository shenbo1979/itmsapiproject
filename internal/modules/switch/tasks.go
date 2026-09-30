// 由 tools/port_switch_batch2a.py 从 switchapi/rpc_client.go 1558-1617 搬运
// （动态任务管理器：嗅探/采集的定时任务调度；LOG 输出改用包内 logf-less 方式）。
package switchmod

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/go-co-op/gocron"
)

// 动态任务管理器
type DynamicTaskManager struct {
	scheduler *gocron.Scheduler
	tasks     sync.Map // 使用 sync.Map 保证并发安全
}

func NewDynamicTaskManager() *DynamicTaskManager {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		location = time.FixedZone("UTC+8", 8*3600)
	}
	return &DynamicTaskManager{
		scheduler: gocron.NewScheduler(location),
	}
}

// Start 启动调度器
func (dtm *DynamicTaskManager) Start() {
	dtm.scheduler.StartAsync()
}

// AddDynamicTask 动态添加任务并返回任务ID
func (dtm *DynamicTaskManager) AddDynamicTask(name, cronExpr string, nType int, taskFunc func()) (string, error) {
	var job interface{}
	var err error
	switch nType {
	case 0:
		job, err = dtm.scheduler.Every(1).Day().At(cronExpr).Tag(name).Do(taskFunc)
	case 1:
		job, err = dtm.scheduler.Every(1).Hour().At(cronExpr).Tag(name).Do(taskFunc)
	default:
		job, err = dtm.scheduler.Cron(cronExpr).Tag(name).Do(taskFunc)
	}

	if err != nil {
		log.Printf("dynamic task register failed: name=%s type=%d expr=%s err=%v", name, nType, cronExpr, err)
		return "", err
	}
	taskID := fmt.Sprintf("%s", name)
	dtm.tasks.Store(taskID, job)
	return taskID, nil
}

// StopTaskByID 停止指定任务
func (dtm *DynamicTaskManager) StopTaskByID(taskID string) bool {
	if job, ok := dtm.tasks.Load(taskID); ok {
		dtm.scheduler.RemoveByReference(job.(*gocron.Job))
		dtm.tasks.Delete(taskID)
		return true
	}
	return false
}

// StopAllTasks 停止所有任务
func (dtm *DynamicTaskManager) StopAllTasks() {
	dtm.tasks.Range(func(key, value interface{}) bool {
		dtm.scheduler.RemoveByReference(value.(*gocron.Job))
		dtm.tasks.Delete(key)
		return true
	})
}
