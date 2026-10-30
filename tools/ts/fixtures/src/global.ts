/** 样例全局脚本——无 import / export（非模块） */

/** 全局计数器 */
var globalCounter = 0;

/**
 * 递增计数
 * @returns 递增后的值
 */
function bumpCounter(): number
{
    globalCounter = globalCounter + 1;
    return globalCounter;
}
