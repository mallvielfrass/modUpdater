package modupdater.lib;

import java.util.List;
import java.util.function.Predicate;

public final class Match {
    private Match() {
    }

    public static int locate(List<LocalJar> local, boolean[] used, String sha, String slug, String filename) {
        int byHash = findLocal(local, used, jar -> sha.equals(jar.sha));
        if (byHash >= 0) {
            return byHash;
        }
        if (slug != null && !slug.isEmpty()) {
            int exact = -1;
            int first = -1;
            for (int i = 0; i < local.size(); i++) {
                if (used[i] || !slug.equals(local.get(i).slug)) {
                    continue;
                }
                if (first < 0) {
                    first = i;
                }
                if (local.get(i).filename.equals(filename)) {
                    exact = i;
                    break;
                }
            }
            if (exact >= 0) {
                return exact;
            }
            if (first >= 0) {
                return first;
            }
        }
        return findLocal(local, used, jar -> filename.equals(jar.filename));
    }

    public static int findLocal(List<LocalJar> local, boolean[] used, Predicate<LocalJar> pred) {
        for (int i = 0; i < local.size(); i++) {
            if (!used[i] && pred.test(local.get(i))) {
                return i;
            }
        }
        return -1;
    }
}
