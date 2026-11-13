package modupdater;

import com.mojang.blaze3d.systems.RenderSystem;

import net.minecraft.client.Minecraft;
import net.minecraft.client.gui.GuiGraphics;
import net.minecraft.client.gui.components.AbstractWidget;
import net.minecraft.client.gui.narration.NarrationElementOutput;
import net.minecraft.network.chat.Component;
import net.minecraft.resources.ResourceLocation;

final class UpdaterButton extends AbstractWidget {
    private static final ResourceLocation WIDGETS = new ResourceLocation("minecraft", "textures/gui/widgets.png");

    UpdaterButton(int x, int y, int width, int height) {
        super(x, y, width, height, Component.literal(ClientHooks.BUTTON));
    }

    @Override
    public void onClick(double mouseX, double mouseY) {
        Minecraft minecraft = Minecraft.getInstance();
        minecraft.setScreen(new ModsScreen(minecraft.screen));
    }

    @Override
    protected void renderWidget(GuiGraphics graphics, int mouseX, int mouseY, float partialTick) {
        boolean alert = ClientHooks.needsRestart();
        setMessage(Component.literal(ClientHooks.BUTTON));
        var font = Minecraft.getInstance().font;
        if (alert) {
            graphics.fill(getX(), getY(), getX() + width, getY() + height, 0xFF3DD68C);
            drawLabel(graphics, font, ClientHooks.BUTTON, getX(), getY(), width, height, 0xFF10261B);
        } else {
            int state = !this.active ? 0 : this.isHoveredOrFocused() ? 2 : 1;
            graphics.setColor(1.0F, 1.0F, 1.0F, this.alpha);
            RenderSystem.enableBlend();
            graphics.blitNineSliced(WIDGETS, getX(), getY(), getWidth(), getHeight(), 20, 4, 200, 20, 0, 46 + state * 20);
            graphics.setColor(1.0F, 1.0F, 1.0F, 1.0F);
            drawLabel(graphics, font, ClientHooks.BUTTON, getX(), getY(), width, height, 0xFFFFFFFF);
        }
        int lineY = getY() + height + 4;
        if (alert) {
            drawNotice(graphics, font, getX(), lineY, ClientHooks.RESTART);
            lineY += WarningMark.SIZE + 4;
        }
        if (ClientHooks.needsRequired()) {
            drawNotice(graphics, font, getX(), lineY, ClientHooks.REQUIRED);
        }
    }

    private static void drawNotice(GuiGraphics graphics, net.minecraft.client.gui.Font font, int x, int y, String text) {
        WarningMark.draw(graphics, x, y);
        graphics.drawString(font, text, x + WarningMark.SIZE + 4, y + (WarningMark.SIZE - 8) / 2, 0xFFFFFFFF);
    }

    private static void drawLabel(GuiGraphics graphics, net.minecraft.client.gui.Font font, String text, int x, int y, int w, int h, int color) {
        int textWidth = font.width(text);
        graphics.drawString(font, text, x + (w - textWidth) / 2, y + (h - 8) / 2, color);
    }

    @Override
    protected void updateWidgetNarration(NarrationElementOutput output) {
        defaultButtonNarrationText(output);
    }
}
