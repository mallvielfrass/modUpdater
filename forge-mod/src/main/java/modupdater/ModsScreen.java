package modupdater;

import java.text.Collator;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.HashSet;
import java.util.List;
import java.util.Locale;
import java.util.Set;

import net.minecraft.client.gui.GuiGraphics;
import net.minecraft.client.gui.components.Button;
import net.minecraft.client.gui.components.EditBox;
import net.minecraft.client.gui.screens.Screen;
import net.minecraft.network.chat.Component;
import net.minecraft.util.Mth;
import net.minecraftforge.fml.ModList;

final class ModsScreen extends Screen {
    private static final int BG = 0xFF14161C;
    private static final int CARD = 0xFF1C1F27;
    private static final int LINE = 0xFF2C313C;
    private static final int TEXT = 0xFFE7E8EA;
    private static final int MUTED = 0xFF9AA0AB;
    private static final int ACCENT = 0xFF3DD68C;
    private static final int INK = 0xFF10261B;
    private static final int AMBER = 0xFFE6B450;
    private static final int BLUE = 0xFF7EB6FF;
    private static final int DANGER = 0xFFF07178;
    private static final int THUMB = 0xFF5C6573;
    private static final String[] FILTERS = {"все", "установленные", "отсутствующие", "устаревшие", "опциональные"};
    private static final String[] SORTS = {"по имени", "сначала новые"};

    private final Screen parent;
    private final String versionText;
    private final List<Click> clicks = new ArrayList<>();
    private EditBox search;
    private volatile List<ModSync.ModView> rows = List.of();
    private volatile boolean auto = true;
    private volatile boolean busy;
    private volatile boolean loading = true;
    private volatile String errorLine = "";
    private volatile String progressLine = "";
    private volatile int progressPct;
    private String query = "";
    private int filter;
    private int sort;
    private boolean requiredOpen;
    private int scroll;
    private int maxScroll;
    private int listTop;
    private int listBottom;
    private int contentHeight;
    private int barX;
    private int barW = 8;
    private int thumbY;
    private int thumbH;
    private boolean draggingBar;
    private int dragGrab;
    private boolean loadStarted;

    ModsScreen(Screen parent) {
        super(Component.literal("Список модов"));
        this.parent = parent;
        this.versionText = ModList.get().getModContainerById(ModUpdater.MOD_ID)
                .map(container -> container.getModInfo().getVersion().toString())
                .orElse("");
    }

    @Override
    protected void init() {
        search = new EditBox(font, 12, 32, 140, 16, Component.literal("поиск"));
        search.setMaxLength(80);
        search.setHint(Component.literal("поиск"));
        search.setTextColor(TEXT);
        search.setBordered(true);
        search.setValue(query);
        search.setResponder(value -> {
            query = value == null ? "" : value;
            scroll = 0;
        });
        addRenderableWidget(search);
        addRenderableWidget(Button.builder(Component.literal("Назад"), button -> minecraft.setScreen(parent))
                .bounds(width / 2 - 100, height - 28, 200, 20)
                .build());
        if (!loadStarted) {
            loadStarted = true;
            startLoad();
        }
    }

    @Override
    public void renderBackground(GuiGraphics graphics) {
        graphics.fill(0, 0, width, height, BG);
    }

    @Override
    public void render(GuiGraphics graphics, int mouseX, int mouseY, float partialTick) {
        renderBackground(graphics);
        clicks.clear();
        int y = 6;
        String ver = versionText.isEmpty() ? "" : "v" + versionText;
        if (!ver.isEmpty()) {
            graphics.drawString(font, ver, 12, y, MUTED);
            graphics.drawString(font, "Список модов", 12 + font.width(ver) + 8, y, TEXT);
        } else {
            graphics.drawString(font, "Список модов", 12, y, TEXT);
        }
        y += 14;
        if (ClientHooks.needsRestart()) {
            y = drawNotice(graphics, 12, y, ClientHooks.RESTART);
        }
        if (ClientHooks.needsRequired()) {
            y = drawNotice(graphics, 12, y, ClientHooks.REQUIRED);
        }
        boolean enabled = !busy && !loading;
        drawToggle(graphics, 12, y);
        y += 16;
        int sortLabel = font.width(SORTS[sort]) + 12;
        int filterLabel = font.width(FILTERS[filter]) + 12;
        int searchW = Math.max(80, width - 24 - filterLabel - sortLabel - 8);
        search.setX(12);
        search.setY(y);
        search.setWidth(searchW);
        drawButton(graphics, 16 + searchW, y, filterLabel, 16, FILTERS[filter], false, true, () -> {
            filter = (filter + 1) % FILTERS.length;
            scroll = 0;
        });
        drawButton(graphics, 20 + searchW + filterLabel, y, sortLabel, 16, SORTS[sort], false, true, () -> {
            sort = (sort + 1) % SORTS.length;
            scroll = 0;
        });
        y += 20;
        List<ModSync.ModView> view = visible();
        List<ModSync.ModView> required = new ArrayList<>();
        List<ModSync.ModView> optional = new ArrayList<>();
        for (ModSync.ModView row : view) {
            if ("current".equals(row.status)) {
                continue;
            }
            if (isRequired(row)) {
                required.add(row);
            } else if (row.clientOnly) {
                optional.add(row);
            }
        }
        int listW = width - 24;
        if (!required.isEmpty()) {
            y += drawRequiredButton(graphics, 12, y, listW, enabled, required) + 4;
            if (requiredOpen) {
                y += drawRequiredPanel(graphics, 12, y, listW, required) + 4;
            }
        } else {
            requiredOpen = false;
        }
        if (!optional.isEmpty()) {
            y += drawWideButton(graphics, 12, y, listW, "Установить необязательные", false, enabled, () -> runUpdate(optional, true)) + 4;
        }
        String progress = progressLine;
        if (progress != null && !progress.isEmpty()) {
            int bar = width - 24;
            graphics.fill(12, y, 12 + bar, y + 6, LINE);
            int fill = (int) (bar * (Math.min(100, Math.max(0, progressPct)) / 100.0));
            graphics.fill(12, y, 12 + fill, y + 6, ACCENT);
            y += 8;
            graphics.drawString(font, progress, 12, y, MUTED);
            y += 12;
        }
        String error = errorLine;
        if (error != null && !error.isEmpty()) {
            graphics.drawString(font, error, 12, y, DANGER);
            y += 12;
        }
        listTop = y;
        listBottom = height - 34;
        barW = 8;
        barX = width - 12 - barW;
        int cardW = Math.max(40, barX - 16);
        if (loading) {
            graphics.drawString(font, "проверка", 12, listTop, MUTED);
        } else if (view.isEmpty() && (error == null || error.isEmpty())) {
            graphics.drawString(font, rows.isEmpty() ? "Список пуст." : "Ничего не найдено.", 12, listTop, MUTED);
        }
        contentHeight = 0;
        for (ModSync.ModView row : view) {
            contentHeight += cardHeight();
        }
        int viewH = Math.max(0, listBottom - listTop);
        maxScroll = Math.max(0, contentHeight - viewH);
        scroll = Mth.clamp(scroll, 0, maxScroll);
        graphics.enableScissor(0, listTop, barX - 2, listBottom);
        int cardY = listTop - scroll;
        for (ModSync.ModView row : view) {
            int cardH = cardHeight();
            if (cardY + cardH >= listTop && cardY <= listBottom) {
                drawCard(graphics, row, 12, cardY, cardW, cardH, enabled);
            }
            cardY += cardH;
        }
        graphics.disableScissor();
        drawScrollbar(graphics);
        super.render(graphics, mouseX, mouseY, partialTick);
    }

    private void drawScrollbar(GuiGraphics graphics) {
        int trackH = Math.max(0, listBottom - listTop);
        if (trackH <= 0) {
            thumbH = 0;
            return;
        }
        graphics.fill(barX, listTop, barX + barW, listBottom, LINE);
        if (maxScroll <= 0) {
            thumbH = trackH;
            thumbY = listTop;
        } else {
            thumbH = Math.max(12, (int) Math.round(trackH * (trackH / (double) Math.max(contentHeight, 1))));
            if (thumbH > trackH) {
                thumbH = trackH;
            }
            int travel = trackH - thumbH;
            thumbY = listTop + (travel <= 0 ? 0 : (int) Math.round(scroll / (double) maxScroll * travel));
        }
        graphics.fill(barX, thumbY, barX + barW, thumbY + thumbH, draggingBar ? ACCENT : THUMB);
    }

    private List<ModSync.ModView> visible() {
        String needle = query.trim().toLowerCase(Locale.ROOT);
        List<ModSync.ModView> out = new ArrayList<>();
        for (ModSync.ModView row : rows) {
            if (!passesFilter(row)) {
                continue;
            }
            if (!needle.isEmpty()) {
                String name = row.name.toLowerCase(Locale.ROOT);
                String file = row.filename.toLowerCase(Locale.ROOT);
                if (!name.contains(needle) && !file.contains(needle)) {
                    continue;
                }
            }
            out.add(row);
        }
        if (sort == 1) {
            out.sort(Comparator.comparingLong((ModSync.ModView row) -> row.createdAt).reversed().thenComparing(row -> row.name, String.CASE_INSENSITIVE_ORDER));
        } else {
            Collator order = Collator.getInstance(new Locale("ru"));
            order.setStrength(Collator.PRIMARY);
            out.sort((left, right) -> {
                int byName = order.compare(left.name, right.name);
                return byName != 0 ? byName : order.compare(left.filename, right.filename);
            });
        }
        return out;
    }

    private boolean passesFilter(ModSync.ModView row) {
        return switch (filter) {
            case 1 -> "current".equals(row.status);
            case 2 -> "missing".equals(row.status);
            case 3 -> "outdated".equals(row.status);
            case 4 -> row.clientOnly && !row.clientRequired;
            default -> true;
        };
    }

    @Override
    public boolean mouseClicked(double mouseX, double mouseY, int button) {
        if (button == 0 && hitBar(mouseX, mouseY)) {
            if (mouseY >= thumbY && mouseY < thumbY + thumbH) {
                draggingBar = true;
                dragGrab = (int) mouseY - thumbY;
            } else if (maxScroll > 0) {
                jumpScroll((int) mouseY);
            }
            return true;
        }
        if (button == 0 && !busy) {
            for (Click click : clicks) {
                if (mouseX >= click.x && mouseX < click.x + click.w && mouseY >= click.y && mouseY < click.y + click.h) {
                    click.action.run();
                    return true;
                }
            }
        }
        return super.mouseClicked(mouseX, mouseY, button);
    }

    @Override
    public boolean mouseDragged(double mouseX, double mouseY, int button, double dragX, double dragY) {
        if (draggingBar && button == 0) {
            int trackH = Math.max(0, listBottom - listTop);
            int travel = trackH - thumbH;
            if (maxScroll > 0 && travel > 0) {
                int y = (int) mouseY - listTop - dragGrab;
                scroll = Mth.clamp((int) Math.round(y / (double) travel * maxScroll), 0, maxScroll);
            }
            return true;
        }
        return super.mouseDragged(mouseX, mouseY, button, dragX, dragY);
    }

    @Override
    public boolean mouseReleased(double mouseX, double mouseY, int button) {
        if (button == 0 && draggingBar) {
            draggingBar = false;
            return true;
        }
        return super.mouseReleased(mouseX, mouseY, button);
    }

    @Override
    public boolean mouseScrolled(double mouseX, double mouseY, double delta) {
        scroll = Mth.clamp(scroll - (int) Math.round(delta * 16), 0, maxScroll);
        return true;
    }

    private boolean hitBar(double mouseX, double mouseY) {
        return mouseX >= barX && mouseX < barX + barW && mouseY >= listTop && mouseY < listBottom;
    }

    private void jumpScroll(int mouseY) {
        int trackH = Math.max(0, listBottom - listTop);
        int travel = trackH - thumbH;
        if (travel <= 0) {
            scroll = 0;
            return;
        }
        int y = mouseY - listTop - thumbH / 2;
        scroll = Mth.clamp((int) Math.round(y / (double) travel * maxScroll), 0, maxScroll);
    }

    private int drawNotice(GuiGraphics graphics, int x, int y, String text) {
        WarningMark.draw(graphics, x, y);
        graphics.drawString(font, text, x + WarningMark.SIZE + 4, y + (WarningMark.SIZE - 8) / 2, TEXT);
        return y + WarningMark.SIZE + 4;
    }

    private void drawToggle(GuiGraphics graphics, int x, int y) {
        boolean on = auto;
        int box = 12;
        graphics.fill(x, y, x + box, y + box, on ? ACCENT : CARD);
        frame(graphics, x, y, box, box, on ? ACCENT : LINE);
        if (on) {
            graphics.drawString(font, "x", x + 3, y + 2, INK);
        }
        graphics.drawString(font, "Автообновление", x + box + 6, y + 2, TEXT);
        clicks.add(new Click(x, y, box + 6 + font.width("Автообновление"), box, this::toggleAuto));
    }

    private void drawCard(GuiGraphics graphics, ModSync.ModView row, int x, int y, int w, int h, boolean enabled) {
        graphics.fill(x, y, x + w, y + h - 4, CARD);
        frame(graphics, x, y, w, h - 4, LINE);
        int textX = x + 6;
        int textY = y + 4;
        int limit = x + w - 8;
        graphics.drawString(font, row.name, textX, textY, TEXT);
        int cursor = textX + font.width(row.name) + 6;
        if (!row.added.isEmpty() && cursor + font.width(row.added) < limit) {
            graphics.drawString(font, row.added, cursor, textY, MUTED);
            cursor += font.width(row.added) + 6;
        }
        if (row.clientOnly && !row.clientRequired) {
            cursor = drawMark(graphics, cursor, textY, limit, "опциональный");
        } else if (isRequired(row)) {
            cursor = drawMark(graphics, cursor, textY, limit, "обязательный");
        }
        if (!row.version.isEmpty() && cursor + font.width(row.version) < limit) {
            graphics.drawString(font, row.version, cursor, textY, MUTED);
        }
        graphics.drawString(font, row.filename, textX, textY + 11, MUTED);
        String status = statusText(row.status);
        graphics.drawString(font, status, textX, textY + 22, statusColor(row.status));
        int bx = textX + font.width(status) + 8;
        if (!"current".equals(row.status)) {
            String label = "missing".equals(row.status) ? "Доустановить" : "Обновить";
            int bw = font.width(label) + 10;
            drawButton(graphics, bx, textY + 20, bw, 14, label, false, enabled, () -> runUpdate(List.of(row), "missing".equals(row.status)));
            bx += bw + 4;
        }
        if (row.configPending) {
            String label = "Накатить конфиг";
            int bw = font.width(label) + 10;
            drawButton(graphics, bx, textY + 20, bw, 14, label, false, enabled, () -> runConfig(row));
        }
        if (!row.localFilename.isEmpty()) {
            String label = "Удалить";
            int bw = font.width(label) + 10;
            drawButton(graphics, x + w - bw - 6, textY + 20, bw, 14, label, false, enabled, () -> runDelete(row));
        }
    }

    private static boolean isRequired(ModSync.ModView row) {
        return !row.clientOnly || row.clientRequired;
    }

    private int drawMark(GuiGraphics graphics, int cursor, int textY, int limit, String mark) {
        int badgeW = font.width(mark) + 8;
        if (cursor + badgeW >= limit) {
            return cursor;
        }
        graphics.fill(cursor, textY - 1, cursor + badgeW, textY + 9, ACCENT);
        graphics.drawString(font, mark, cursor + 4, textY, INK);
        return cursor + badgeW + 6;
    }

    private static int cardHeight() {
        return 46;
    }

    private void drawButton(GuiGraphics graphics, int x, int y, int w, int h, String label, boolean primary, boolean enabled, Runnable action) {
        int bg = primary && enabled ? ACCENT : CARD;
        int fg = !enabled ? MUTED : primary ? INK : TEXT;
        graphics.fill(x, y, x + w, y + h, bg);
        frame(graphics, x, y, w, h, primary && enabled ? ACCENT : LINE);
        drawFitted(graphics, label, x, y, w, h, fg);
        if (enabled) {
            clicks.add(new Click(x, y, w, h, action));
        }
    }

    private int drawWideButton(GuiGraphics graphics, int x, int y, int listW, String label, boolean primary, boolean enabled, Runnable action) {
        int inner = listW - 16;
        String[] lines = wrap(label, inner);
        int h = lines.length == 1 ? 18 : 30;
        boolean green = primary && enabled;
        int bg = green ? ACCENT : CARD;
        int fg = !enabled ? MUTED : green ? 0xFF14161C : TEXT;
        graphics.fill(x, y, x + listW, y + h, bg);
        frame(graphics, x, y, listW, h, green ? ACCENT : LINE);
        int textHeight = lines.length * 10;
        int textY = y + (h - textHeight) / 2;
        for (String line : lines) {
            int textWidth = font.width(line);
            int textX = x + (listW - textWidth) / 2;
            if (green) {
                graphics.drawString(font, line, textX, textY, fg, false);
            } else {
                graphics.drawString(font, line, textX, textY, fg);
            }
            textY += 10;
        }
        if (enabled) {
            clicks.add(new Click(x, y, listW, h, action));
        }
        return h;
    }

    private int drawRequiredButton(GuiGraphics graphics, int x, int y, int listW, boolean enabled, List<ModSync.ModView> mods) {
        int toggleW = 18;
        int textW = listW - toggleW;
        String[] lines = wrap("Доустановить обязательное", textW - 16);
        int h = lines.length == 1 ? 18 : 30;
        graphics.fill(x, y, x + listW, y + h, enabled ? ACCENT : CARD);
        frame(graphics, x, y, listW, h, enabled ? ACCENT : LINE);
        graphics.fill(x + textW, y, x + textW + 1, y + h, 0xFF14161C);
        int fg = enabled ? 0xFF14161C : MUTED;
        int textHeight = lines.length * 10;
        int textY = y + (h - textHeight) / 2;
        for (String line : lines) {
            int lineW = font.width(line);
            graphics.drawString(font, line, x + (textW - lineW) / 2, textY, fg, false);
            textY += 10;
        }
        drawChevron(graphics, x + textW + (toggleW - 7) / 2, y + (h - 5) / 2, requiredOpen, fg);
        if (enabled) {
            clicks.add(new Click(x, y, textW, h, () -> runUpdate(mods, true)));
        }
        clicks.add(new Click(x + textW, y, toggleW, h, () -> requiredOpen = !requiredOpen));
        return h;
    }

    private int drawRequiredPanel(GuiGraphics graphics, int x, int y, int w, List<ModSync.ModView> mods) {
        if (mods.isEmpty()) {
            graphics.fill(x, y, x + w, y + 16, CARD);
            frame(graphics, x, y, w, 16, LINE);
            graphics.drawString(font, "список пуст", x + 6, y + 4, MUTED);
            return 16;
        }
        int rowH = 22;
        int h = 4 + mods.size() * rowH + 4;
        graphics.fill(x, y, x + w, y + h, CARD);
        frame(graphics, x, y, w, h, LINE);
        int textY = y + 4;
        for (ModSync.ModView row : mods) {
            graphics.drawString(font, row.name, x + 6, textY, TEXT);
            graphics.drawString(font, row.filename, x + 6, textY + 10, MUTED);
            clicks.add(new Click(x, textY, w, rowH, () -> {
            }));
            textY += rowH;
        }
        return h;
    }

    private void drawChevron(GuiGraphics graphics, int x, int y, boolean open, int color) {
        for (int i = 0; i < 4; i++) {
            int dy = open ? 3 - i : i;
            graphics.fill(x + i, y + dy, x + i + 2, y + dy + 2, color);
            graphics.fill(x + 6 - i, y + dy, x + 8 - i, y + dy + 2, color);
        }
    }

    private String[] wrap(String label, int maxWidth) {
        if (font.width(label) <= maxWidth) {
            return new String[] {label};
        }
        int split = label.lastIndexOf(' ');
        if (split <= 0 || split >= label.length() - 1) {
            return new String[] {label};
        }
        return new String[] {label.substring(0, split), label.substring(split + 1)};
    }

    private void drawFitted(GuiGraphics graphics, String label, int x, int y, int w, int h, int color) {
        int maxWidth = w - 8;
        String[] lines = wrap(label, maxWidth);
        int textHeight = lines.length * 10;
        int textY = y + Math.max(0, (h - textHeight) / 2);
        for (String line : lines) {
            int textWidth = font.width(line);
            graphics.drawString(font, line, x + (w - textWidth) / 2, textY, color);
            textY += 10;
        }
    }

    private static void frame(GuiGraphics graphics, int x, int y, int w, int h, int color) {
        graphics.fill(x, y, x + w, y + 1, color);
        graphics.fill(x, y + h - 1, x + w, y + h, color);
        graphics.fill(x, y, x + 1, y + h, color);
        graphics.fill(x + w - 1, y, x + w, y + h, color);
    }

    private static String statusText(String status) {
        if ("current".equals(status)) {
            return "установлен";
        }
        if ("outdated".equals(status)) {
            return "заменить";
        }
        return "доустановить";
    }

    private static int statusColor(String status) {
        if ("current".equals(status)) {
            return ACCENT;
        }
        if ("outdated".equals(status)) {
            return AMBER;
        }
        return BLUE;
    }

    private void toggleAuto() {
        boolean next = !auto;
        auto = next;
        Thread thread = new Thread(() -> {
            try {
                ModSync.setAutoUpdate(next);
            } catch (Exception ex) {
                minecraft.execute(() -> {
                    auto = !next;
                    errorLine = "не удалось сохранить";
                });
            }
        }, "modupdater-auto");
        thread.setDaemon(true);
        thread.start();
    }

    private void startLoad() {
        loading = true;
        errorLine = "";
        Thread thread = new Thread(() -> {
            try {
                boolean choice = ModSync.autoUpdate();
                minecraft.execute(() -> auto = choice);
                List<ModSync.ModView> next = ModSync.inspect();
                minecraft.execute(() -> {
                    auto = choice;
                    rows = next;
                    loading = false;
                });
            } catch (Exception ex) {
                minecraft.execute(() -> {
                    loading = false;
                    errorLine = "config".equals(ex.getMessage()) ? ClientHooks.CONFIG_FAIL : ClientHooks.FAIL;
                });
            }
        }, "modupdater-list");
        thread.setDaemon(true);
        thread.start();
    }

    private void runUpdate(List<ModSync.ModView> chosen, boolean configsOnMissing) {
        if (busy || chosen.isEmpty()) {
            return;
        }
        List<String> ids = new ArrayList<>();
        Set<String> configs = new HashSet<>();
        for (ModSync.ModView row : chosen) {
            if ("current".equals(row.status)) {
                continue;
            }
            ids.add(row.id);
            if (configsOnMissing && "missing".equals(row.status)) {
                configs.add(row.id);
            }
        }
        if (ids.isEmpty()) {
            return;
        }
        busy = true;
        errorLine = "";
        progressLine = "";
        progressPct = 0;
        Thread thread = new Thread(() -> finishJob(() -> ModSync.update(ids, configs, (label, pct) -> {
            progressLine = label;
            progressPct = pct;
        }), "не удалось обновить моды"), "modupdater-install");
        thread.setDaemon(true);
        thread.start();
    }

    private void runConfig(ModSync.ModView row) {
        if (busy) {
            return;
        }
        busy = true;
        errorLine = "";
        progressLine = "";
        progressPct = 0;
        Thread thread = new Thread(() -> finishJob(() -> ModSync.applyConfig(row.id, (label, pct) -> {
            progressLine = label;
            progressPct = pct;
        }), ClientHooks.CONFIG_FAIL), "modupdater-config");
        thread.setDaemon(true);
        thread.start();
    }

    private void runDelete(ModSync.ModView row) {
        if (busy || row.localFilename.isEmpty()) {
            return;
        }
        busy = true;
        errorLine = "";
        progressLine = "";
        progressPct = 0;
        Thread thread = new Thread(() -> finishJob(() -> ModSync.hideLocal(row.localFilename), "не удалось удалить"), "modupdater-delete");
        thread.setDaemon(true);
        thread.start();
    }

    private void finishJob(Job job, String fallback) {
        String fail = null;
        try {
            job.run();
        } catch (Exception ex) {
            fail = "config".equals(ex.getMessage()) ? ClientHooks.CONFIG_FAIL : fallback;
        }
        String error = fail;
        try {
            List<ModSync.ModView> next = ModSync.inspect();
            minecraft.execute(() -> {
                rows = next;
                busy = false;
                progressLine = "";
                progressPct = 0;
                if (error != null) {
                    errorLine = error;
                }
            });
        } catch (Exception ex) {
            minecraft.execute(() -> {
                busy = false;
                progressLine = "";
                errorLine = error == null ? ClientHooks.FAIL : error;
            });
        }
    }

    private record Click(int x, int y, int w, int h, Runnable action) {
    }

    @FunctionalInterface
    private interface Job {
        void run() throws Exception;
    }
}
