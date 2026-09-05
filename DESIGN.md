---
name: ManyRouter 运营控制台
description: 高密度、多站点、以业务状态为核心的运营工作台
colors:
  primary: "#18634a"
  primary-hover: "#124c39"
  primary-soft: "#e2f1e9"
  page: "#f5f7f6"
  surface: "#ffffff"
  surface-muted: "#eff3f1"
  text: "#202a26"
  text-muted: "#58665f"
  border: "#d7dfda"
  navigation: "#202a27"
  navigation-text: "#d6e0db"
  navigation-active: "#344a3f"
  danger: "#b32934"
  danger-soft: "#ffebed"
  warning: "#845908"
  warning-soft: "#fff2cf"
  info: "#235b92"
  info-soft: "#e7f0fb"
typography:
  headline:
    fontFamily: '"Segoe UI", "Microsoft YaHei", "PingFang SC", sans-serif'
    fontSize: "1.375rem"
    fontWeight: 650
    lineHeight: 1.35
    letterSpacing: "0"
  title:
    fontFamily: '"Segoe UI", "Microsoft YaHei", "PingFang SC", sans-serif'
    fontSize: "1rem"
    fontWeight: 650
    lineHeight: 1.5
    letterSpacing: "0"
  body:
    fontFamily: '"Segoe UI", "Microsoft YaHei", "PingFang SC", sans-serif'
    fontSize: "0.875rem"
    fontWeight: 400
    lineHeight: 1.5
    letterSpacing: "0"
  label:
    fontFamily: '"Segoe UI", "Microsoft YaHei", "PingFang SC", sans-serif'
    fontSize: "0.75rem"
    fontWeight: 600
    lineHeight: 1.5
    letterSpacing: "0"
rounded:
  control: "4px"
  dialog: "8px"
spacing:
  "1": "0.25rem"
  "2": "0.5rem"
  "3": "0.75rem"
  "4": "1rem"
  "6": "1.5rem"
  "8": "2rem"
components:
  button-primary:
    backgroundColor: "{colors.primary}"
    textColor: "{colors.surface}"
    rounded: "{rounded.control}"
    padding: "0.5rem 0.75rem"
    height: "2.25rem"
  button-primary-hover:
    backgroundColor: "{colors.primary-hover}"
    textColor: "{colors.surface}"
    rounded: "{rounded.control}"
  button-danger:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.danger}"
    rounded: "{rounded.control}"
    padding: "0.5rem 0.75rem"
    height: "2.25rem"
  button-danger-hover:
    backgroundColor: "{colors.danger-soft}"
    textColor: "{colors.danger}"
    rounded: "{rounded.control}"
  input:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.text}"
    rounded: "{rounded.control}"
    padding: "0.5rem 0.75rem"
    height: "2.25rem"
  navigation-item-active:
    backgroundColor: "{colors.navigation-active}"
    textColor: "{colors.surface}"
    rounded: "{rounded.control}"
    padding: "0.75rem"
  status-success:
    backgroundColor: "{colors.primary-soft}"
    textColor: "{colors.primary}"
    rounded: "{rounded.control}"
    padding: "2px 0.5rem"
  table-header:
    backgroundColor: "{colors.surface-muted}"
    textColor: "{colors.text-muted}"
    padding: "0.5rem 1rem"
    height: "2.75rem"
  notice-warning:
    backgroundColor: "{colors.warning-soft}"
    textColor: "{colors.warning}"
    rounded: "{rounded.control}"
    padding: "0.75rem 1rem"
  dialog:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.text}"
    rounded: "{rounded.dialog}"
    padding: "1.5rem"
    width: "min(680px, calc(100% - 32px))"
---

# Design System: ManyRouter 运营控制台

## Overview

**Creative North Star: "运营台账"**

ManyRouter 的界面像一份持续更新的运营台账：导航位置稳定，站点范围持续在场，表格提供紧凑对比，编辑与确认只在需要时浮出。它服务高频操作，优先保证归属清楚、控件位置稳定、状态可以核对。

视觉由石墨绿导航、浅灰工作底色和白色操作面构成。品牌感来自深绿命令色、克制的小圆角与整齐的行列；界面不加入营销区块、说明卡、装饰图表或大号宣传标题。适用范围为 `web/` 内全部运营界面；产品事实见 [PRODUCT.md](./PRODUCT.md)，业务职责见 [运营控制台](./docs/模块设计/运营控制台.md)。

**Key Characteristics:**

- 高密度列表、短工具栏和页内站点范围，适合反复扫描与操作。
- 绿色承载常规命令与肯定状态，红色承载危险停用与失败，蓝色承载同步和历史确认。
- 桌面使用常驻侧栏，移动端使用模态导航；两端保持同一信息顺序。
- 加载、失败、空结果、处理中和已确认都有独立的文字与视觉反馈。

## Colors

色彩以低饱和中性色构成工作面，用少量绿、红、黄、蓝表达动作和状态。

### Primary

- **深运营绿** (`#18634a`)：主要保存、新增、发布和同步命令，以及成功状态。
- **压暗运营绿** (`#124c39`)：主要命令的悬停与按下反馈。
- **浅确认绿** (`#e2f1e9`)：成功徽标和成功提示的底色。

### Secondary

- **石墨导航** (`#202a27`)：桌面侧栏、移动导航和工具提示的底色。
- **柔白导航文字** (`#d6e0db`)：导航默认文字、图标与侧栏焦点轮廓。
- **苔绿选中面** (`#344a3f`)：当前导航项的稳定选中状态。

### Tertiary

- **危险红** (`#b32934`) 与 **危险浅红** (`#ffebed`)：失败、停用、关闭入口、恢复线路等需要谨慎确认的动作。
- **待处理黄** (`#845908`) 与 **待处理浅黄** (`#fff2cf`)：待同步、人工处理、入口关闭提示等未完成状态。
- **核对蓝** (`#235b92`) 与 **核对浅蓝** (`#e7f0fb`)：同步中状态和“最近确认”历史标记。

### Neutral

- **工作底色** (`#f5f7f6`)：页面背景，让白色操作面保持轻微层次。
- **操作面白** (`#ffffff`)：顶栏、输入、表格、弹窗与按钮表面。
- **静默表面** (`#eff3f1`)：表头、禁用输入、默认徽标和加载骨架。
- **石墨正文** (`#202a26`) 与 **次要文字** (`#58665f`)：正文、标签、说明和时间信息。
- **浅分隔线** (`#d7dfda`)：输入边界、表格分隔和区域边界。

**The 稀缺强调色 Rule.** 绿色只标记可执行的常规命令与肯定状态；红色只标记危险动作与失败；蓝色只标记同步过程和历史核对信息。

## Typography

**UI Font:** `Segoe UI`，依次回退到 `Microsoft YaHei`、`PingFang SC` 和系统无衬线字体。

**Character:** 字体保持中性、紧凑和清晰。页面不使用展示型字体，层级依靠固定字号、字重、间距和位置建立。

### Hierarchy

- **Headline** (650, 22px, 1.35): 页面主标题，每页只承担当前业务对象名称。
- **Title** (650, 16px, 1.5): 弹窗标题、区段标题和紧凑信息组标题。
- **Body** (400, 14px, 1.5): 表格、表单、按钮和主要业务内容。
- **Label** (600, 12px, 1.5): 表头、徽标与辅助标签；辅助说明保持 400 字重。
- **Brand** (700, 19px): 只用于侧栏中的 ManyRouter 名称。

金额、倍率、版本和分页使用等宽数字。长名称、地址和标识允许换行，表格操作与短状态保持单行。

**The 固定字号 Rule.** 字号不随视口宽度缩放；移动端通过换行、单列布局与横向表格容纳内容。

## Layout

全局间距按 4、8、12、16、24、32px 递进。控件最小高度为 36px，表格行基准高度为 44px，页面内容最大宽度为 1800px。

桌面宽度大于 960px 时，页面使用 224px 常驻侧栏和剩余宽度工作区。侧栏吸附在视口顶部并占满视口高度；56px 顶栏位于正常页面流中，持续展示站点选择和账户操作。内容区使用 32px 内边距，列表负责扫描，弹窗负责编辑、发布、恢复和其他需要集中注意力的任务。

宽度不超过 960px 时，侧栏收进最长 304px 的模态抽屉，背景进入遮罩，焦点留在导航内，关闭后回到菜单按钮。顶栏左右内边距缩为 16px，内容区使用 24px 上下、16px 左右内边距。

宽度不超过 640px 时，工具栏和搜索框占满可用宽度，双列表单变为单列，弹窗内边距缩为 16px。表格保留至少 680px 的数据宽度并在独立容器横向滚动；“操作”列吸附在右侧，首屏即可操作，表格下方持续显示双向箭头提示。

**The 站点在场 Rule.** 站点相关页面在标题或确认内容中显示当前站点，切换站点后重新建立该站点的编辑上下文。

**The 移动操作可达 Rule.** 手机端行操作始终停留在可见区域，数据列仍可横向滚动，滚动容器保留可见滚动条和键盘入口。

## Elevation & Depth

系统默认保持平面。浅灰页面、白色操作面、静默表头与 1px 分隔线承担大部分层次；普通业务区不使用阴影，也不包装成漂浮卡片。模态弹窗使用一组结构性阴影（`0 16px 56px #17291d33`），弹窗和移动导航都配合半透明深绿遮罩（`#14201980`）隔离背景任务。

### Shadow Vocabulary

- **模态浮层** (`0 16px 56px #17291d33`): 只用于需要中断当前操作流的弹窗。

**The 平面工作台 Rule.** 静态业务区域依靠底色和分隔线分层，阴影只服务模态浮层。

## Shapes

控件、导航项、徽标、提示和表格滚动条使用紧凑的 4px 圆角；弹窗使用 8px 圆角。数据表只保留顶部、底部和行间分隔，业务区不使用大圆角容器。图标按钮为 36px 正方形，常用图标为 16–18px。

**The 小圆角 Rule.** 小圆角用于表达可操作边界，不把页面区段塑造成独立卡片。

## Components

### Buttons

- **主要命令：** 深绿色实底、白色文字、4px 圆角和 36px 最小高度；用于新增、保存、发布和同步。
- **普通命令：** 白色表面与浅灰描边；用于取消、查看和次要操作。
- **危险命令：** 危险红文字与红色描边，悬停进入浅红底色；站点停用、Auto 停用和站点供应商下线分别显示电源图标与“保存并停用站点”“保存并停用 Auto”“保存并下线供应商”。关闭入口、关闭用户入口和恢复线路沿用同一危险语言。
- **处理中：** 操作按钮替换为加载图标并禁用；图标以 0.8 秒线性旋转，禁用态整体降至 55% 不透明度。系统开启减少动态效果后停止动画与过渡。
- **图标按钮：** 36px 正方形，必须带可访问名称和悬停提示。

### Inputs / Fields

输入框、选择器和搜索框使用白色表面、1px 浅灰描边、4px 圆角和 36px 最小高度。悬停时描边加深，聚焦时使用 2px 深绿外轮廓；禁用态使用静默表面。标签始终可见，提示文字放在控件下方。

### Navigation

桌面侧栏常驻并按 4px 间距排列导航项；默认项使用柔白文字，悬停时加深底色，当前项使用苔绿选中面与白色文字。移动端沿用同一顺序和样式，通过 Base UI 模态抽屉约束焦点，点击导航项后关闭。

### Tables

表头使用静默表面、12px 半粗文字和 44px 高度；数据单元格使用 12px × 16px 内边距与行间分隔。名称与标识分成主次两行，数字右对齐并使用等宽数字。加载时显示三行骨架，失败时用红色提示替代表格，真实空结果显示空状态。

### Status, Notices, and Empty States

状态徽标使用文字和颜色双重表达：绿为肯定，红为失败，蓝为进行中，黄为等待或人工处理。提示条使用同色浅底与图标。通用加载状态带 `role="status"`、礼貌播报和“正在读取数据”的读屏文字；站点范围进一步区分读取中、读取失败、没有站点、等待选择和可操作。

### Dialogs

标准弹窗最宽 680px，宽版最宽 960px，并限制在视口高度内滚动。写请求进行时，弹窗声明忙碌状态，禁用右上角关闭按钮，并阻止 Escape、遮罩点击和关闭事件；表单按钮同步禁用，失败后保留输入。

### Entry Access and Price Confirmation

专属分组只使用“入口开放/入口关闭”，Auto 只使用“用户入口开放/用户入口关闭”。入口关闭时显示黄色提示“关闭后，该组已有密钥也无法调用。”，提交关闭动作使用红色危险按钮。

售价列表把“最近确认”显示为蓝色历史徽标，并在同一列显示历史成功核对时间；它记录历史核对结果，不承诺当前外部售价仍处于该状态。草稿保存和发布保持两个独立动作。

**The 请求状态 Rule.** 服务端确认前不显示成功；处理中锁定提交上下文，失败时保留输入并提供可执行的重试入口。

**The 历史确认 Rule.** “最近确认”始终与核对时间一起出现，任何待同步或失败状态都不能借用它表达当前外部状态。

## Do's and Don'ts

### Do:

- **Do** 保持站点范围、目标对象和动作结果可见。
- **Do** 用绿色表达常规命令，用红色表达危险停用，用状态文字补足颜色含义。
- **Do** 在手机端保留右侧操作列、横向滚动条和双向滚动提示。
- **Do** 明确区分加载、失败、空结果、处理中和服务端确认成功。
- **Do** 完整使用入口开放语义，并把“最近确认”限定为历史核对信息。

### Don't:

- **Don't** 添加营销区块、说明卡、虚构指标、装饰图表或大号宣传标题。
- **Don't** 把普通业务区包装成漂浮卡片，也不要为静态区域添加阴影。
- **Don't** 让手机端行操作随着数据列滚出首屏。
- **Don't** 允许写请求进行时关闭弹窗或重复提交。
- **Don't** 把本地提交、待同步或历史核对展示成当前外部状态。
