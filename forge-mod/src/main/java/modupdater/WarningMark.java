package modupdater;

import net.minecraft.client.gui.GuiGraphics;

final class WarningMark {
    static final int SIZE = 28;
    private static final int YELLOW = 0xFFF5C400;
    private static final int DARK = 0xFF1A1400;

    private WarningMark() {
    }

    static void draw(GuiGraphics graphics, int x, int y) {
        int size = SIZE;
        int tip = size - 1;
        for (int row = 0; row < size; row++) {
            int half = row * (size / 2) / tip;
            graphics.fill(x + size / 2 - half, y + row, x + size / 2 + half + 1, y + row + 1, YELLOW);
        }
        int cx = x + size / 2;
        int bar = Math.max(2, size / 10);
        int barTop = y + size / 4;
        int barBottom = y + (size * 2) / 3;
        graphics.fill(cx - bar / 2, barTop, cx + (bar + 1) / 2, barBottom, DARK);
        int dot = Math.max(2, bar);
        int dotY = barBottom + Math.max(2, size / 12);
        graphics.fill(cx - dot / 2, dotY, cx + (dot + 1) / 2, Math.min(y + size - 2, dotY + dot), DARK);
    }
}
