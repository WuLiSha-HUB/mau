import { _decorator, Component, Label } from 'cc';

const { ccclass, property } = _decorator;

/** 字幕层——按时间轴切段显示 */
@ccclass('SubtitleLayer')
export class SubtitleLayer extends Component
{
    /** 字幕文本 */
    @property(Label)
    public label: Label | null = null;

    /** 当前字幕键 */
    private currentKey: string = '';

    /**
     * 显示一条字幕
     * @param key 字幕键
     * @param text 字幕文本
     */
    public Show(key: string, text: string): void
    {
        this.currentKey = key;
        if (this.label !== null)
        {
            this.label.string = text;
        }
    }

    /** 清空字幕 */
    public Clear(): void
    {
        this.currentKey = '';
        if (this.label !== null)
        {
            this.label.string = '';
        }
    }

    /**
     * 当前字幕键
     * @returns 字幕键；未显示返回空串
     */
    public Current(): string
    {
        return this.currentKey;
    }
}
