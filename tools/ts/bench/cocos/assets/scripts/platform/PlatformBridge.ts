import { _decorator, Component, sys } from 'cc';

const { ccclass, property } = _decorator;

/** 平台桥——微信小游戏与浏览器两种运行面差异的收敛点 */
@ccclass('PlatformBridge')
export class PlatformBridge extends Component
{
    /** 是否为微信小游戏环境 */
    @property
    public isMinigame: boolean = false;

    /** 用户标识（登录后写入） */
    private openId: string = '';

    /** 初始化平台面 */
    public Init(): void
    {
        this.isMinigame = sys.platform === sys.Platform.WECHAT_GAME;
    }

    /**
     * 写用户标识
     * @param openId 平台用户标识
     */
    public SetOpenId(openId: string): void
    {
        this.openId = openId;
    }

    /**
     * 用户标识是否已就绪
     * @returns 已登录即 true
     */
    public HasOpenId(): boolean
    {
        return this.openId.length > 0;
    }
}
