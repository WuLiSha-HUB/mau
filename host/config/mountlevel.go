package config

import (
	"strconv"
	"strings"
	"sync/atomic"
)

// 全局默认挂载档位（mount.level）的进程内投影——「立即生效」语义的落点。
//
// 与 llm.reasoning_effort 平行：那是思考强度的全局回落（每猫 reasoningEffort 为空时用），
// 本项是挂载档位的全局回落（每猫 projectMountLevel 未设置时用）。
//
// 0 = 未配置（回落每猫 projectMountExpert 兼容位 / 硬默认专家）；1/2/3 = 普通/专家/精通。
// 用 atomic 而非锁——ResolveMountLevel 在每轮注入 / 每次自报头都会读，必须无锁。
var gDefaultMountLevel atomic.Int32

// MountLevelConfigKey 是全局挂载档位的 schema 键（唯一真相源在 schema.json）。
const MountLevelConfigKey = "mount.level"

// SetDefaultMountLevel 更新全局回落档位——配置写盘（POST /api/v1/config、config-set）
// 与宿主启动加载两处调用。非法值（0 / >3 / 非数字）清 0（回落每猫兼容位）。
func SetDefaultMountLevel(level int) {
	if level >= MountLevelNormal && level <= MountLevelMaster {
		gDefaultMountLevel.Store(int32(level))
	} else {
		gDefaultMountLevel.Store(0)
	}
}

// DefaultMountLevel 读全局回落档位（0 = 未配置）。
func DefaultMountLevel() int { return int(gDefaultMountLevel.Load()) }

// MountLevelFromSetting 把配置值字符串（schema 值域 "1"/"2"/"3"）转档位；非法返回 0。
func MountLevelFromSetting(value string) int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n < MountLevelNormal || n > MountLevelMaster {
		return 0
	}
	return n
}

// SyncDefaultMountLevelFromStore 从全局配置读 mount.level 并灌入进程内投影——宿主启动后调用一次。
// store / schema 未绑定时清 0（回落每猫兼容位，行为与没有本配置项前一致）。
func SyncDefaultMountLevelFromStore(store *Store, schema *Schema) {
	if store == nil || schema == nil || schema.Find(MountLevelConfigKey) == nil {
		SetDefaultMountLevel(0)
		return
	}
	SetDefaultMountLevel(MountLevelFromSetting(store.Resolve(schema, MountLevelConfigKey).Value))
}
