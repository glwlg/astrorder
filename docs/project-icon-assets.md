# 项目拟态图标

这组六枚图标通过内置 imagegen 生成。应用只使用
`frontend/public/project-icons/` 中的透明 WebP，不包含生成原图。

## 交付规格

- 尺寸：128 × 128，保留透明通道。
- 编码：WebP，质量 86，压缩方法 6，无附加元数据。
- 总体积：35,468 字节；单张 4,902–6,468 字节。
- 主色定义：`frontend/src/components/projectAppearanceCatalog.ts`；
  服务端保存时在 `app-server-go/internal/preferences/preferences.go` 绑定同一颜色。

| 文件 | 图标 | 固定主色 |
| --- | --- | --- |
| orbit.webp | 星环 | blue / #2563eb |
| prism.webp | 紫晶 | violet / #7c3aed |
| rocket.webp | 启航 | red / #e11d48 |
| layers.webp | 叠光 | emerald / #059669 |
| cube.webp | 冰方 | cyan / #0891b2 |
| flower.webp | 绽放 | fuchsia / #c026d3 |

## 生成提示词

统一要求：production UI project icon; glossy rounded translucent acrylic/glass,
soft studio highlights, sculptural depth in the same material family as the
Astrorder star-and-orbit logo; strong simple silhouette readable at 20px;
one centered object, about 80% frame coverage; true transparent background,
clean alpha cutout, no white matte, ground plane, text, watermark or particles.

各图标的主体与配色提示：

- **星环**：a compact three-dimensional violet-blue glass planet with a sweeping
  coral-pink ring; cobalt blue dominant, purple secondary, pink ring;
  front three-quarter view.
- **紫晶**：a rounded violet crystal/prism with three large faceted faces,
  slightly bulging edges, dominant violet #7c3aed, lilac and small cobalt
  reflections; front three-quarter view.
- **启航**：a compact rounded rocket pointing diagonally up and right,
  coral rose #e11d48 fuselage, violet fins, cool blue porthole,
  a small attached rose exhaust droplet.
- **叠光**：a stack of three rounded square glass slabs, diagonally offset
  with layered edges visible, emerald #059669, luminous mint highlights,
  teal shadow edges; front three-quarter view.
- **冰方**：a single chunky rounded glass cube, cyan #0891b2, aqua highlights,
  small cobalt reflections, slightly inflated edges and three broad visible
  faces; front three-quarter view.
- **绽放**：a compact abstract five-petal glass rosette with a small central
  opening, fuchsia #c026d3, rose highlights and purple shadows;
  front view, no stem or leaves.
