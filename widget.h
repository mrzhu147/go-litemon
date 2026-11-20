#ifndef LITEMON_WIDGET_H
#define LITEMON_WIDGET_H

#ifdef __cplusplus
extern "C" {
#endif

/* Go 侧每次采集后填这个结构体，交给 Qt 绘制 */
typedef struct {
    char host[128];      /* 定长拷贝，避免指针悬空 */
    double cpu;          /* % */
    double mem;          /* % */
    double load1;
    double up_bps;       /* B/s */
    double down_bps;     /* B/s */
    double read_kbps;    /* KB/s */
    double write_kbps;   /* KB/s */
    double disk_pct;     /* % */
    double disk_used_gb;
    double disk_total_gb;
    double gpu;          /* %，-1 表示 N/A */
    double gpumem;       /* %，-1 表示 N/A */
    int    autostart;    /* 1=已开启自启 */
} LMMetrics;

/* 先初始化 QApplication（必须在创建任何 widget 之前、在主线程调用） */
int lm_app_init(int argc, char **argv);

/* 创建透明置顶面板 */
void *lm_create(int x, int y, int w, int h);

/* 推送一帧数据并触发重绘（可从任意线程调用，内部用 invokeMethod 切到 GUI 线程） */
void lm_update(void *panel, const LMMetrics *m);

/* 同步更新并立即重绘（仅在 GUI 线程调用，截图自检用） */
void lm_update_sync(void *panel, const LMMetrics *m);

/* 进入 Qt 事件循环（阻塞，必须在主线程调用） */
int lm_run(void *panel, int argc, char **argv);

/* 离屏渲染一帧到 png（用于无桌面环境自检），成功返回 0 */
int lm_screenshot(void *panel, const char *path);

#ifdef __cplusplus
}
#endif
#endif
