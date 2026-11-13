package modupdater;

import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.atomic.AtomicBoolean;

import net.minecraft.client.Minecraft;
import net.minecraft.client.gui.components.AbstractWidget;
import net.minecraft.client.gui.screens.TitleScreen;
import net.minecraft.network.chat.Component;
import net.minecraft.network.chat.contents.TranslatableContents;
import net.minecraftforge.api.distmarker.Dist;
import net.minecraftforge.client.event.ScreenEvent;
import net.minecraftforge.eventbus.api.EventPriority;
import net.minecraftforge.eventbus.api.SubscribeEvent;
import net.minecraftforge.fml.common.Mod;
import net.minecraftforge.fml.loading.FMLPaths;

@Mod.EventBusSubscriber(modid = ModUpdater.MOD_ID, bus = Mod.EventBusSubscriber.Bus.FORGE, value = Dist.CLIENT)
final class ClientHooks {
    static final String RESTART = "перезагрузите майнкрафт";
    static final String REQUIRED = "есть обязательные моды";
    static final String BUTTON = "mod updater";
    static final String BUTTON_RESTART = "mod updater (перезагрузите майнкрафт)";
    static final String FAIL = "не удалось проверить моды";
    static final String CONFIG_FAIL = "не удалось обновить конфиги";

    private static final AtomicBoolean HOOKED = new AtomicBoolean();
    private static final AtomicBoolean STARTED = new AtomicBoolean();
    private static volatile String line = "";
    private static volatile boolean requiredPending;

    private ClientHooks() {
    }

    static void init() {
        ModSync.bind(FMLPaths.GAMEDIR.get(), FMLPaths.MODSDIR.get());
        if (HOOKED.compareAndSet(false, true)) {
            Runtime.getRuntime().addShutdownHook(new Thread(ModSync::onShutdown, "modupdater-swap"));
        }
    }

    static void publish(String text) {
        String next = text == null ? "" : text;
        Minecraft mc = Minecraft.getInstance();
        if (mc == null) {
            line = next;
            return;
        }
        mc.execute(() -> line = next);
    }

    static boolean needsRestart() {
        return RESTART.equals(line);
    }

    static void publishRequired(boolean pending) {
        Minecraft mc = Minecraft.getInstance();
        if (mc == null) {
            requiredPending = pending;
            return;
        }
        mc.execute(() -> requiredPending = pending);
    }

    static boolean needsRequired() {
        return requiredPending;
    }

    @SubscribeEvent(priority = EventPriority.LOWEST)
    public static void onInit(ScreenEvent.Init.Post event) {
        if (!(event.getScreen() instanceof TitleScreen screen)) {
            return;
        }
        if (STARTED.compareAndSet(false, true)) {
            Thread thread = new Thread(ModSync::run, "modupdater-sync");
            thread.setDaemon(true);
            thread.start();
        }
        placeButton(event);
    }

    private static void placeButton(ScreenEvent.Init.Post event) {
        List<AbstractWidget> widgets = new ArrayList<>();
        AbstractWidget single = null;
        AbstractWidget mods = null;
        AbstractWidget realms = null;
        AbstractWidget options = null;
        for (var child : event.getListenersList()) {
            if (!(child instanceof AbstractWidget widget)) {
                continue;
            }
            widgets.add(widget);
            String key = translationKey(widget.getMessage());
            String text = widget.getMessage().getString();
            if ("menu.singleplayer".equals(key) || "Одиночная игра".equals(text) || "Singleplayer".equals(text)) {
                single = widget;
            } else if ("fml.menu.mods".equals(key) || "Моды".equals(text) || "Mods".equals(text)) {
                mods = widget;
            } else if ("menu.online".equals(key) || text.contains("Realms")) {
                realms = widget;
            } else if ("menu.options".equals(key) || "Настройки...".equals(text) || "Options...".equals(text)) {
                options = widget;
            }
        }
        if (single == null) {
            return;
        }
        int rowY = mods != null ? mods.getY() : realms != null ? realms.getY() : single.getY() + 48;
        int buttonY = rowY + 24;
        int below = buttonY + 20 + 4 + (WarningMark.SIZE + 4) * 2;
        if (options != null && options.getY() < below) {
            int from = options.getY();
            int delta = below - from;
            for (AbstractWidget widget : widgets) {
                if (widget.getY() == from) {
                    widget.setY(widget.getY() + delta);
                }
            }
        }
        event.addListener(new UpdaterButton(single.getX(), buttonY, single.getWidth(), 20));
    }

    private static String translationKey(Component component) {
        if (component.getContents() instanceof TranslatableContents contents) {
            return contents.getKey();
        }
        return "";
    }

    @SubscribeEvent
    public static void onRender(ScreenEvent.Render.Post event) {
        if (!(event.getScreen() instanceof TitleScreen screen)) {
            return;
        }
        String text = line;
        if (text == null || text.isEmpty() || RESTART.equals(text)) {
            return;
        }
        event.getGuiGraphics().drawString(Minecraft.getInstance().font, text, screen.width / 2 - Minecraft.getInstance().font.width(text) / 2, 8, 0xFFFF55);
    }
}
