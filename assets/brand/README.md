# 星序品牌图标

`astrorder-mark.png` 是当前唯一的透明品牌母版。网页、PWA、Windows 窗口和托盘图标均从该文件导出，README 也直接引用母版。

配色采用 IBM Design Language / Carbon：Blue 60 `#0F62FE`、Purple 60 `#8A3FFC`、Magenta 50 `#EE5396`。星体从蓝色过渡到紫色，轨道使用洋红色，保留立体造型及透明背景。

重新导出：

```sh
python3 scripts/build_brand_icons.py
```

导出脚本使用 Pillow，仅进行缩放与格式转换，并以预乘透明度处理边缘。保留 RGBA 通道，不填充背景色，不通过亮度阈值抠图或 CSS 羽化模拟透明。正式输出位于 `frontend/public/` 和 `desktop-wails/assets/`；浅色与深色文件名仍由应用引用，均来自同一母版。

配色来源：IBM Design Language 的 Color 色板。
