// A292 压测载具生成器——合成 TS 工程（规模可控、可重建、确定性）
// 用法：node gen_bench.js --out <dir> [--files N] [--with-error]
// 结构：tsconfig.json + src/model.ts + src/types/global.d.ts + src/node_XXXX.ts 链式引用 + 可选 broken.ts
'use strict';

var fs = require('fs');
var nodePath = require('path');

/**
 * 解析命令行参数
 * @param {string[]} argv 进程参数
 * @returns {object} 参数表
 */
function parseArgs(argv)
{
    var result = { out: '', files: 300, withError: false };
    for (var i = 2; i < argv.length; i = i + 1)
    {
        var key = argv[i];
        if (key === '--out' && i + 1 < argv.length)
        {
            result.out = argv[i + 1];
            i = i + 1;
        }
        else if (key === '--files' && i + 1 < argv.length)
        {
            result.files = parseInt(argv[i + 1], 10);
            i = i + 1;
        }
        else if (key === '--with-error')
        {
            result.withError = true;
        }
    }
    return result;
}

/**
 * 数字补零（文件名排序稳定）
 * @param {number} value 数值
 * @param {number} width 位宽
 * @returns {string} 补零文本
 */
function pad(value, width)
{
    var text = String(value);
    while (text.length < width)
    {
        text = '0' + text;
    }
    return text;
}

/**
 * 生成一期模块源码（第 i 个，链式引用前一个）
 * @param {number} index 序号（1 起）
 * @returns {string} 源码文本
 */
function buildNode(index)
{
    var name = pad(index, 4);
    // 链式深度上限 20——真实工程不会出现 1500 层类型嵌套；深链会让 TS 类型展开爆炸，
    // 掩盖被测服务本身的成本（载具缺陷会伪装成产品缺陷）
    var chained = index > 1 && (index - 1) % 20 !== 0;
    var lines = [];
    lines.push("import { Entity, Kind, Box } from './model';");
    if (chained)
    {
        lines.push("import { Item" + pad(index - 1, 4) + ", make" + pad(index - 1, 4) + " } from './node_" + pad(index - 1, 4) + "';");
    }
    lines.push('');
    lines.push('/** 第 ' + name + ' 期条目——块内链式依赖上一期，形成有界依赖图 */');
    lines.push('export interface Item' + name + ' extends Entity');
    lines.push('{');
    lines.push('    payload: string;');
    if (chained)
    {
        lines.push('    ref: Item' + pad(index - 1, 4) + ';');
    }
    lines.push('}');
    lines.push('');
    lines.push('/** 构造第 ' + name + ' 期条目 */');
    lines.push('export function make' + name + '(id: number): Item' + name);
    lines.push('{');
    if (chained)
    {
        lines.push('    return { id: id, kind: Kind.Beta, name: \'n' + name + '\', payload: \'p' + name + '\', ref: make' + pad(index - 1, 4) + '(id + 1) };');
    }
    else
    {
        lines.push('    return { id: id, kind: Kind.Alpha, name: \'n' + name + '\', payload: \'p' + name + '\' } as Item' + name + ';');
    }
    lines.push('}');
    lines.push('');
    lines.push('/** 描述第 ' + name + ' 期条目（泛型容器往返） */');
    lines.push('export function describe' + name + '(v: Item' + name + '): string');
    lines.push('{');
    lines.push('    var box = new Box<Item' + name + '>(v);');
    lines.push('    return box.get().name + \':\' + String(box.get().id);');
    lines.push('}');
    lines.push('');
    return lines.join('\n');
}

/**
 * 主流程——按参数生成工程
 */
function main()
{
    var args = parseArgs(process.argv);
    if (!args.out)
    {
        process.stderr.write('缺少参数 --out <dir>\n');
        process.exit(2);
    }
    var outDir = nodePath.resolve(args.out);
    var srcDir = nodePath.join(outDir, 'src');
    var typesDir = nodePath.join(srcDir, 'types');
    fs.rmSync(srcDir, { recursive: true, force: true });
    fs.mkdirSync(typesDir, { recursive: true });

    // [段1] 工程口径——与 fixtures 同构（paths 别名 + strict + noEmit）
    var tsconfig = {
        compilerOptions: {
            target: 'ES2020',
            module: 'commonjs',
            moduleResolution: 'node',
            strict: true,
            noEmit: true,
            baseUrl: '.',
            paths: { '@/*': ['src/*'] }
        },
        include: ['src/**/*.ts']
    };
    fs.writeFileSync(nodePath.join(outDir, 'tsconfig.json'), JSON.stringify(tsconfig, null, 2) + '\n', 'utf8');

    // [段2] 公共模型
    var model = [
        '/** 合成工程公共模型——枚举 / 接口 / 泛型容器 */',
        'export enum Kind',
        '{',
        "    Alpha = 'alpha',",
        "    Beta = 'beta'",
        '}',
        '',
        '/** 实体基接口 */',
        'export interface Entity',
        '{',
        '    id: number;',
        '    kind: Kind;',
        '    name: string;',
        '}',
        '',
        '/** 泛型容器——往返取值 */',
        'export class Box<T>',
        '{',
        '    private value: T;',
        '',
        '    constructor(value: T)',
        '    {',
        '        this.value = value;',
        '    }',
        '',
        '    /** 取值 */',
        '    get(): T',
        '    {',
        '        return this.value;',
        '    }',
        '}',
        ''
    ].join('\n');
    fs.writeFileSync(nodePath.join(srcDir, 'model.ts'), model, 'utf8');

    // [段3] 环境声明（.d.ts 进编译面）
    var globalDts = [
        '/** 环境声明——全局命名空间（不进任何模块） */',
        'declare namespace Bench',
        '{',
        '    /** 构建元信息 */',
        '    interface Meta',
        '    {',
        '        build: string;',
        '        version: number;',
        '    }',
        '}',
        ''
    ].join('\n');
    fs.writeFileSync(nodePath.join(typesDir, 'global.d.ts'), globalDts, 'utf8');

    // [段4] 写面靶子——patched 时改这里的一行
    var target = [
        '/** 写面靶子——压测 ts.patch 用（标记行唯一） */',
        'export var benchFlag = 0;',
        '',
        '/** 加法（被 patch 波及的语义面） */',
        'export function benchAdd(a: number, b: number): number',
        '{',
        '    return a + b + benchFlag;',
        '}',
        ''
    ].join('\n');
    fs.writeFileSync(nodePath.join(srcDir, 'bench_target.ts'), target, 'utf8');

    // [段5] 模块链
    for (var i = 1; i <= args.files; i = i + 1)
    {
        fs.writeFileSync(nodePath.join(srcDir, 'node_' + pad(i, 4) + '.ts'), buildNode(i), 'utf8');
    }

    // [段6] 可选负例
    if (args.withError)
    {
        var broken = [
            'import { Entity } from \'./model\';',
            '',
            '/** 故意类型错误——压测负例面 */',
            'export const broken: Entity = { id: \'text\', kind: \'alpha\', name: \'x\' };',
            ''
        ].join('\n');
        fs.writeFileSync(nodePath.join(srcDir, 'broken.ts'), broken, 'utf8');
    }

    process.stdout.write(JSON.stringify({
        ok: true,
        out: outDir,
        files: args.files + 3,
        withError: args.withError
    }) + '\n');
}

main();
