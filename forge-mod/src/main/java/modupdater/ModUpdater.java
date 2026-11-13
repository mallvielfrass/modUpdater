package modupdater;

import net.minecraftforge.api.distmarker.Dist;
import net.minecraftforge.fml.common.Mod;
import net.minecraftforge.fml.loading.FMLEnvironment;

@Mod(ModUpdater.MOD_ID)
public final class ModUpdater {
    public static final String MOD_ID = "modupdater";

    public ModUpdater() {
        if (FMLEnvironment.dist != Dist.CLIENT) {
            return;
        }
        ClientHooks.init();
    }
}
