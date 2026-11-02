import { _decorator, Component, director } from 'cc';
import { PlayerKernel } from '../player/PlayerKernel';

const { ccclass, property } = _decorator;

/** 启动器——装配内核并进入主界面 */
@ccclass('GameBoot')
export class GameBoot extends Component
{
    /** 播放内核 */
    @property(PlayerKernel)
    public kernel: PlayerKernel | null = null;

    /** 启动完成回调 */
    protected start(): void
    {
        if (this.kernel === null)
        {
            return;
        }
        this.kernel.Prepare(() =>
        {
            director.loadScene('MainMenu');
        });
    }
}
