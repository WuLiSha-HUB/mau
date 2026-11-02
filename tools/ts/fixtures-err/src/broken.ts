/** 故意错误样例——供 ts.check 负例 */

export interface Payload
{
    /** 数量 */
    count: number;
}

/**
 * 类型不匹配（string 塞进 number 字段）
 * @returns 载荷
 */
export function broken(): Payload
{
    return { count: 'not-a-number' };
}
