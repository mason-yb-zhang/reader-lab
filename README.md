# 墨页实验室 · reader-lab

面向快易典 C1-Slim / MP-D261 的独立电子书阅读器，基于 C1auncher Book Reader 0.1.23 修改。应用 ID 为 `reader-lab`，作者显示名为 `mason-yb-zhang`。不是原厂固件，也不替换官方 `book-reader`。

## 功能与按键

- TXT / EPUB / MOBI / AZW3 纯文字阅读，支持目录、百分比跳转和书签。MOBI/AZW3 首次打开在设备内解析并写入私有缓存，不修改原书，不需要电脑预转换。
- MOBI 支持无压缩、PalmDOC、HUFF/CDIC；AZW3 支持独立 KF8 和包含 KF8 的混合 MOBI，按 skeleton/fragment 索引重组正文。章节目录由文档分段及 HTML 标题生成，不承诺完全复制原书导航树。
- 书架按真实文件夹逐层浏览，只列当前层的文件夹和书籍，文件夹优先。Enter/右键进入，Back/左键返回父文件夹，根目录才退出；从阅读返回保留原文件夹和选中项。空文件夹可进入，符号链接不显示，不移动或重命名书籍。
- 书架任意文件夹按 **R** 打开“最近阅读”，最多显示最近20本；按 **OK/右键**直接恢复上次阅读位置。兼容已有进度记录，已删除或移出书库的书自动忽略，返回后保留原文件夹。
- 正文按 **S** 打开设置，**上下**选择项目、**左右**调整，**Back**返回正文。
- 正文 **OK / F** 切换全屏，重新分页并保持页首字节位置。
- 内置 GNU Unifont（16px原生，其他档位缩放）和 Fusion Pixel 原生12px。
- 附带霞鹜文楷轻便版 Regular，支持12/14/16/18/20/24px；字体按需加载，Linux用只读mmap，避免完整堆拷贝峰值。
- 黑白阈值32–224，默认128；点阵字形不受阈值影响。
- 设置、进度与书签保存在独立目录，不读取或迁移官方阅读器存档。
- 退出恢复本程序修改的刷新参数，正确释放运行租约；不结束父进程。

书架/目录 **Enter或右键**打开，正文 **上下/音量键**翻页，**右键**添加或删除书签，**O**百分比跳转，**Back/左键**逐级返回。回到书架再按Back退出。

## 设备与安装

需要支持 C1ancher 外部应用租约的设备环境，验证基线为 C1ancher 2.9.10。目标为Linux/MIPS32小端、o32、硬浮点双精度、静态ELF；不是Windows EXE或Android APK。

社区发布完成后，可在应用商店刷新并搜索 **墨页实验室 / reader-lab** 安装。仓库中存在源码或版本标签，不单独证明社区服务已接受发布；以设备列表和服务端结果为准。

应用从商店启动时复用包管理器传入的运行/硬件租约；手动执行时独立申请，其他应用占用时拒绝启动，不强行夺取屏幕。

```mermaid
flowchart LR
    A[应用列表] --> B[c1pkg传入租约]
    B --> C[reader-lab]
    C --> D[读取书籍与绘制]
    D --> E[独立存档]
```

### 文件位置

| 内容 | 路径 |
| --- | --- |
| 默认书库 | `/storage/mtp/Book`，包含子目录 |
| 进度、书签、设置 | `/storage/c1/reader-lab/state` |
| 转换缓存 | `/storage/c1/reader-lab/state/documents` |
| 设置文件 | `/storage/c1/reader-lab/state/reader-settings.json` |
| 附带字体 | 安装包内 `assets/fonts/`，由程序真实执行路径定位 |

不写 `/etc`、不改启动脚本、不修改核心公钥，不覆盖官方阅读器。早期独立测试目录与此应用使用相同的实验室存档位置，演示书和正式书籍按路径及指纹区分。

环境变量可显式覆盖 `C1_BOOKS_DIR`、`C1_BOOK_READER_HOME`、`C1BOOK_READER_CACHE_DIR`、`C1_LAB_FONTS_DIR`，仍受原0.1.23存储路径检查约束。开发诊断 `C1_LAB_CAPTURE=1` 会在实验室存档目录记录最近一帧；默认关闭。

## 构建

需要Python 3和Go 1.26或兼容更新版本：

```text
python build.py --go <go可执行文件> --version 0.1.0
```

脚本运行两个模块的主机测试与vet，再交叉编译并检查ELF属性，生成 `build/v0.1.0/payload`、校验清单和构建报告。输出目录必须不存在，避免覆盖已冻结版本。脚本不会调用发布器、联网注册作者或访问ADB。

两个Go模块的相对路径必须保留。固定依赖见 `go.mod/go.sum`；`third-party/goproxy` 保留上游版本的依赖源码材料，新增格式解析所需版本应先在两个模块目录执行 `go mod download` 填充模块缓存，再进行离线构建。字体位图已提交，正常构建无需重新生成。

原生12px字库生成：安装Pillow/fontTools后运行 `python App/book-reader/tools/build_pixel12.py --check`，或去掉 `--check` 重新生成。源字体、完整OFL及组件许可保留在 `assets/fusion-pixel-12px`。

## 验证与限制

- 已在Windows主机执行单元测试和静态检查，并完成MIPS ELF核验。
- 独立测试版在C1设备上验证了按键事件、全屏、原生12px、文楷加载、字号、阈值、正常退出以及设置/进度重开恢复。
- 继承租约使用真实exec子进程测试，在设备上验证通过。店铺发布后的安装/启动仍需以实际验收为准。
- 这些检查不等同于全部实体按键手感、残影、掉电、长书、大量字体或长期功耗验收。
- mmap中的字体不能原地截断或改写；更换字体前退出阅读器，或使用新文件加原子rename。
- Fusion12保留36,278个字形，280个超出固定字框的源字形被排除并使用替代符号；不保证全部Unicode覆盖。
- 矢量字体小字号转黑白可能断笔或粘连，字号和阈值需要按实屏选择。
- 固定文件路径的安全检查不承诺抵御具有root权限的并发篡改。
- MOBI/AZW3 不支持 DRM、Print Replica、ORDT 特殊索引编码、缺少必要索引的 KF8；畸形片段严格报错。只提取正文，不渲染图片、字体、CSS 或复杂表格，不保证全部电子书兼容。
- MOBI/AZW3 限制源文件 512MiB、解压正文 256MiB、单个 HTML token 256KiB，并对记录、字典、索引和章节数量设限；这些是拒绝阈值，不代表设备可同时容纳同等大小的内存。
- 新格式回归入口为 `go test -count=1 ./...`。可选 `MOBI_TEST_SAMPLES` 指向 libmobi 的本地 `tests/samples`，`MOBI_TEST_SPLIT` 指向 KindleUnpack 拆分的独立 MOBI/AZW3；未设置时相应外部样本测试跳过。测试缓存、进度和书签使用独立临时目录，不打包私人书籍。

## 来源与许可

主代码基于 [fwz233-RE/C1auncher](https://github.com/fwz233-RE/C1auncher) Book Reader 0.1.23对应源码，沿用 **GPL v3**，见 `LICENSE`。修改包括独立名称与存档、全屏、设备设置、字体管理、mmap、租约兼容及相关测试。

- GNU Unifont：见 `App/book-reader/assets/font-LICENSE.txt`。
- Fusion Pixel Font简体12px：源自 [Kasiin/C1-Slim-Ports](https://github.com/Kasiin/C1-Slim-Ports) 所附资源，上游 [TakWolf/fusion-pixel-font](https://github.com/TakWolf/fusion-pixel-font)，OFL及组件许可完整保留。
- 霞鹜文楷轻便版 Regular v1.522：[lxgw/LxgwWenKai-Lite](https://github.com/lxgw/LxgwWenKai-Lite)，许可见 `assets/fonts/OFL.txt`。
- Go运行时与扩展库的BSD许可说明见 `App/book-reader/THIRD_PARTY_NOTICES.md` 及依赖源码材料。

本仓库不包含私人书籍、设备照片、设备序列号、MAC、API密钥、发布令牌或设备备份。请勿把这些内容加入应用包或源码仓库。
