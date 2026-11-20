// 原生 Qt5 Widgets 面板：透明、无边框、可直接浮在 UOS/Deepin 桌面上。
// 不依赖任何浏览器内核（QWidget + QPainter 直接绘制）。
#include "widget.h"

#include <QApplication>
#include <QFont>
#include <QFontDatabase>
#include <QFontMetrics>
#include <QMenu>
#include <QMouseEvent>
#include <QPainter>
#include <QPainterPath>
#include <QStandardPaths>
#include <QTimer>
#include <QWidget>
#include <QDir>
#include <QFile>
#include <QFileInfo>
#include <cstring>

namespace {

const char *kAutostartName = "go-litemon.desktop";

QString autostartPath() {
    QString dir = QStandardPaths::writableLocation(QStandardPaths::ConfigLocation) + "/autostart";
    return dir + "/" + kAutostartName;
}

bool autostartEnabled() { return QFile::exists(autostartPath()); }

bool setAutostart(bool on) {
    QString p = autostartPath();
    if (!on) {
        QFile::remove(p);
        return true;
    }
    QDir().mkpath(QFileInfo(p).absolutePath());
    QString exe = QCoreApplication::applicationFilePath();
    QFile f(p);
    if (!f.open(QIODevice::WriteOnly | QIODevice::Truncate))
        return false;
    f.write("[Desktop Entry]\n"
            "Type=Application\n"
            "Name=go-litemon\n"
            "Comment=轻量性能面板\n"
            "Exec=" + exe.toUtf8() + "\n"
            "X-GNOME-Autostart-enabled=true\n"
            "Terminal=false\n");
    f.close();
    return true;
}

QString humanKB(double kb) {
    if (kb >= 1024.0 * 1024.0) return QString::number(kb / 1024.0 / 1024.0, 'f', 1) + "GB";
    if (kb >= 1024.0) return QString::number(kb / 1024.0, 'f', 1) + "MB";
    return QString::number(qRound(kb)) + "KB";
}

QString humanBytes(double b) {
    const char *u[] = {"B", "K", "M", "G", "T"};
    int i = 0;
    double n = b;
    while (n >= 1024.0 && i < 4) { n /= 1024.0; i++; }
    return QString::number(n, 'f', (i >= 3 || n >= 100) ? 1 : 0) + u[i];
}

// 找一个真正含中文字形的字体：先按路径 addApplicationFont（绕开 fontconfig），再退回族名探测
QFont cjkFont() {
    static QFont cached;
    static bool ready = false;
    if (ready) return cached;
    ready = true;

    const QChar probe(0x5185); // 「内」
    const bool debug = qEnvironmentVariableIsSet("LM_DEBUG");

    QStringList paths = {
        "/usr/share/fonts/google-droid-fonts/DroidSansFallback.ttf",
        "/usr/share/fonts/truetype/droid/DroidSansFallbackFull.ttf",
        "/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
        "/usr/share/fonts/truetype/wqy/wqy-microhei.ttc",
        "/usr/share/fonts/truetype/wqy/wqy-zenhei.ttc"
    };
    // 再扫一遍常见目录，兜住发行版差异（UOS/Deepin 常见位置）
    QStringList dirs;
    dirs << "/usr/share/fonts" << "/usr/local/share/fonts" << QDir::homePath() + "/.fonts";
    for (const QString &dir : dirs) {
        QDir d(dir);
        if (!d.exists()) continue;
        for (const QFileInfo &fi : d.entryInfoList(QStringList() << "*.ttf" << "*.ttc" << "*.otf", QDir::Files, QDir::Name)) {
            if (fi.fileName().contains("CJK", Qt::CaseInsensitive) ||
                fi.fileName().contains("Droid", Qt::CaseInsensitive) ||
                fi.fileName().contains("wqy", Qt::CaseInsensitive) ||
                fi.fileName().contains("Han", Qt::CaseInsensitive) ||
                fi.fileName().contains("SimSun", Qt::CaseInsensitive) ||
                fi.fileName().contains("Ukai", Qt::CaseInsensitive) ||
                fi.fileName().contains("Uming", Qt::CaseInsensitive)) {
                paths << fi.absoluteFilePath();
            }
        }
    }

    for (const QString &p : paths) {
        if (!QFile::exists(p)) continue;
        int id = QFontDatabase::addApplicationFont(p);
        if (id < 0) continue;
        const QStringList fams = QFontDatabase::applicationFontFamilies(id);
        if (fams.isEmpty()) continue;
        QFont f(fams.first());
        if (QFontMetrics(f).inFont(probe)) {
            f.setPointSizeF(QApplication::font().pointSizeF() * 0.95);
            cached = f;
            if (debug) qWarning("go-litemon: CJK font = %s (%s)", qPrintable(fams.first()), qPrintable(p));
            return cached;
        }
    }

    // 退路：遍历已注册族名找含字形的
    for (const QString &p : QFontDatabase().families()) {
        QFont f(p);
        if (QFontMetrics(f).inFont(probe)) {
            f.setPointSizeF(QApplication::font().pointSizeF() * 0.95);
            cached = f;
            if (debug) qWarning("go-litemon: CJK font(fallback) = %s", qPrintable(p));
            return cached;
        }
    }
    cached = QApplication::font();
    if (debug) qWarning("go-litemon: no CJK font found, using %s", qPrintable(cached.family()));
    return cached;
}

QColor levelColor(double pct) {
    if (pct >= 88) return QColor(248, 113, 113);
    if (pct >= 70) return QColor(250, 204, 21);
    return QColor(74, 222, 128);
}

class Panel : public QWidget {
public:
    LMMetrics m{};
    QPoint dragOffset;
    bool dragging = false;

    Panel() {
        setAttribute(Qt::WA_TranslucentBackground, true);
        setAttribute(Qt::WA_ShowWithoutActivating, true);
        setWindowFlags(Qt::FramelessWindowHint | Qt::Tool | Qt::WindowStaysOnTopHint);
        setWindowTitle("go-litemon");
        resize(620, 176);
    }

    void applyMetrics(const LMMetrics &v) {
        m = v;
        update();
    }

protected:
    void paintEvent(QPaintEvent *) override {
        QPainter p(this);
        p.setRenderHint(QPainter::Antialiasing, true);
        p.setRenderHint(QPainter::TextAntialiasing, true);

        const int pad = 14;
        const QColor fg(215, 222, 232);
        const QColor dim(127, 139, 155);
        const QColor track(255, 255, 255, 26);

        // 品牌行
        QFont brand = font();
        brand.setBold(true);
        brand.setLetterSpacing(QFont::AbsoluteSpacing, 1.6);
        brand.setFamily("DejaVu Sans Mono");
        brand.setPointSizeF(font().pointSizeF() * 0.95);
        p.setFont(brand);
        p.setPen(QColor(159, 179, 200));
        p.drawText(pad, pad + 14, "LITEMON");

        QFont small = font();
        small.setFamily("DejaVu Sans Mono");
        small.setStyleHint(QFont::Monospace);
        small.setPointSizeF(font().pointSizeF() * 0.92);

        // 中文标签专用字体：自动挑一个真的含 CJK 字形的族
        QFont cjk = cjkFont();
        p.setFont(cjk);
        p.setPen(dim);
        p.drawText(pad + 86, pad + 14, QString::fromUtf8(m.host));

        // 自启状态（右上角小圆点 + 文字）
        bool autoOn = autostartEnabled();
        int dotX = width() - pad - 62;
        p.setPen(Qt::NoPen);
        p.setBrush(autoOn ? QColor(74, 222, 128) : QColor(100, 116, 139));
        p.drawEllipse(QPoint(dotX, pad + 10), 3, 3);
        p.setPen(dim);
        p.setFont(cjk);
        p.drawText(dotX + 9, pad + 14, autoOn ? QString::fromUtf8("自启 开") : QString::fromUtf8("自启 关"));

        // ---- 指标区（三列，无温度）----
        QFont val = small;
        val.setBold(true);
        const int rowH = 23;
        int y = pad + 34;
        const int cols[3] = {pad, width() / 3, width() * 2 / 3};
        const int labelW = 44, valW = 112;

        auto cell = [&](int cx, int cy, const QString &label, const QString &text, const QColor &c, bool cjkLabel = false) {
            p.setFont(cjkLabel ? cjk : small);
            p.setPen(dim);
            p.drawText(cx, cy + 18, label);
            p.setFont(val);
            p.setPen(c);
            QRect r(cx + labelW, cy, valW, rowH);
            p.drawText(r, Qt::AlignRight | Qt::AlignVCenter, text);
        };

        const QColor na(75, 85, 99);
        struct Cell { QString label; QString val; QColor color; bool cjk; };
        const Cell table[3][3] = {
            {{"CPU", QString::number(m.cpu, 'f', 1) + "%", fg, false},
             {"GPU", m.gpu < 0 ? "N/A" : QString::number(m.gpu, 'f', 1) + "%", m.gpu < 0 ? na : fg, false},
             {QString::fromUtf8("显存"), m.gpumem < 0 ? "N/A" : QString::number(m.gpumem, 'f', 1) + "%", m.gpumem < 0 ? na : fg, true}},
            {{QString::fromUtf8("内存"), QString::number(m.mem, 'f', 1) + "%", fg, true},
             {QString::fromUtf8("负载"), QString::number(m.load1, 'f', 2), fg, true},
             {"", "", fg, false}},
            {{QString::fromUtf8("读"), humanKB(m.read_kbps) + "/s", fg, true},
             {QString::fromUtf8("写"), humanKB(m.write_kbps) + "/s", fg, true},
             {QString::fromUtf8("网速"), humanKB(m.up_bps / 1024.0) + "↑ " + humanKB(m.down_bps / 1024.0) + "↓", fg, true}},
        };
        for (int r = 0; r < 3; ++r) {
            for (int col = 0; col < 3; ++col) {
                const Cell &cc = table[r][col];
                if (cc.label.isEmpty()) continue;
                cell(cols[col], y, cc.label, cc.val, cc.color, cc.cjk);
            }
            y += rowH;
        }

        // ---- 磁盘：单行总空间 ----
        y += rowH + 6;
        p.setFont(cjk);
        p.setPen(dim);
        p.drawText(pad, y + 16, QString::fromUtf8("磁盘"));

        int barX = pad + labelW;
        int barW = width() - barX - pad - 60 - 170;
        if (barW < 60) barW = 60;
        QRect bar(barX, y + 5, barW, 8);
        QPainterPath bp;
        bp.addRoundedRect(bar, 4, 4);
        p.fillPath(bp, track);
        double pct = m.disk_pct;
        if (pct < 0) pct = 0;
        if (pct > 100) pct = 100;
        QRect fill(bar.x(), bar.y(), int(bar.width() * pct / 100.0), bar.height());
        QPainterPath fp;
        fp.addRoundedRect(fill, 4, 4);
        p.fillPath(fp, levelColor(pct));

        p.setFont(val);
        p.setPen(pct >= 88 ? QColor(248, 113, 113) : (pct >= 70 ? QColor(250, 204, 21) : fg));
        p.drawText(QRect(bar.right() + 8, y, 52, rowH), Qt::AlignLeft | Qt::AlignVCenter,
                   QString::number(qRound(pct)) + "%");

        p.setPen(dim);
        p.drawText(QRect(width() - pad - 168, y, 168, rowH), Qt::AlignRight | Qt::AlignVCenter,
                   humanBytes(m.disk_used_gb * 1024.0 * 1024.0 * 1024.0) + " / " +
                       humanBytes(m.disk_total_gb * 1024.0 * 1024.0 * 1024.0));
    }

    void mousePressEvent(QMouseEvent *e) override {
        if (e->button() == Qt::LeftButton) {
            dragging = true;
            dragOffset = e->globalPos() - frameGeometry().topLeft();
            e->accept();
        }
    }
    void mouseMoveEvent(QMouseEvent *e) override {
        if (dragging) { move(e->globalPos() - dragOffset); e->accept(); }
    }
    void mouseReleaseEvent(QMouseEvent *e) override {
        if (e->button() == Qt::LeftButton) dragging = false;
    }

    void contextMenuEvent(QContextMenuEvent *e) override {
        QMenu menu(this);
        QAction *a = menu.addAction(QString::fromUtf8("开机自启"));
        a->setCheckable(true);
        a->setChecked(autostartEnabled());
        menu.addSeparator();
        QAction *q = menu.addAction(QString::fromUtf8("退出"));
        QAction *chosen = menu.exec(e->globalPos());
        if (chosen == a) setAutostart(!autostartEnabled());
        else if (chosen == q) qApp->quit();
        update();
    }
};

Panel *g_panel = nullptr;
QApplication *g_app = nullptr;

} // namespace

extern "C" int lm_app_init(int argc, char **argv) {
    if (g_app) return 0;
    static int s_argc = 1;
    static char *s_argv[4];
    if (argc > 0) {
        s_argc = argc;
        for (int i = 0; i < argc && i < 4; ++i) s_argv[i] = argv[i];
    } else {
        static char name[] = "go-litemon";
        s_argv[0] = name;
    }
    g_app = new QApplication(s_argc, s_argv);
    g_app->setApplicationName("go-litemon");
    return 0;
}

extern "C" void *lm_create(int x, int y, int w, int h) {
    if (!g_panel) g_panel = new Panel();
    if (w > 0 && h > 0) g_panel->resize(w, h);
    g_panel->move(x, y);
    g_panel->show();
    return g_panel;
}

extern "C" void lm_update(void *panel, const LMMetrics *m) {
    if (!panel || !m) return;
    Panel *p = static_cast<Panel *>(panel);
    LMMetrics copy = *m;
    QMetaObject::invokeMethod(p, [p, copy]() { p->applyMetrics(copy); }, Qt::QueuedConnection);
}

extern "C" void lm_update_sync(void *panel, const LMMetrics *m) {
    if (!panel || !m) return;
    Panel *p = static_cast<Panel *>(panel);
    p->applyMetrics(*m);
    p->repaint();
}

extern "C" int lm_run(void *panel, int argc, char **argv) {
    static int s_argc = 0;
    static char **s_argv = nullptr;
    if (argc > 0) { s_argc = argc; s_argv = argv; }
    (void)panel;
    return QApplication::exec();
}

extern "C" int lm_screenshot(void *panel, const char *path) {
    if (!panel) return 1;
    Panel *p = static_cast<Panel *>(panel);
    p->show();
    QCoreApplication::processEvents();
    QPixmap pm = p->grab();
    return pm.save(QString::fromUtf8(path), "PNG") ? 0 : 2;
}
