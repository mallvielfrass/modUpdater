import { describe, expect, it } from "vitest";
import { filterLogLines, isChatLine, isErrorLine, isSessionLine, logFileName } from "./logLines";

const join =
  "2026-11-06T14:00:00+0300 host java[9]: [06Nov2026 14:00:00.100] [Server thread/INFO] [net.minecraft.server.players.PlayerList/]: Steve joined the game";
const leave =
  "2026-11-06T14:01:00+0300 host java[9]: [06Nov2026 14:01:00.100] [Server thread/INFO] [net.minecraft.server.players.PlayerList/]: Alex left the game";
const chat =
  "2026-11-06T14:02:00+0300 host java[9]: [06Nov2026 14:02:00.100] [Server thread/INFO] [net.minecraft.server.network.ServerGamePacketListenerImpl/]: <Steve> привет";
const unsigned =
  "2026-11-06T14:02:01+0300 host java[9]: [06Nov2026 14:02:01.100] [Server thread/INFO] [net.minecraft.server.network.ServerGamePacketListenerImpl/]: [Not Secure] <Steve> hi";
const plainChat = "2026-11-06T14:02:02+0300 host java[9]: <Steve> hello";
const error =
  "2026-11-06T14:03:00+0300 host java[9]: [06Nov2026 14:03:00.100] [Server thread/ERROR] [net.minecraft.server.MinecraftServer/]: Tick took too long";
const fatal =
  "2026-11-06T14:03:01+0300 host java[9]: [06Nov2026 14:03:01.100] [Server thread/FATAL] [net.minecraftforge.server.ServerLifecycleHooks/]: Crash";
const exception = "2026-11-06T14:03:02+0300 host java[9]: java.lang.NullPointerException: test";
const caused = "2026-11-06T14:03:03+0300 host java[9]: Caused by: java.lang.IllegalStateException: closed";
const stack = "2026-11-06T14:03:04+0300 host java[9]: \tat net.minecraft.server.MinecraftServer.run(MinecraftServer.java:616)";
const warn =
  "2026-11-06T14:04:00+0300 host java[9]: [06Nov2026 14:04:00.100] [Server thread/WARN] [net.minecraft.server.MinecraftServer/]: Can't keep up";
const info =
  "2026-11-06T14:05:00+0300 host java[9]: [06Nov2026 14:05:00.100] [Server thread/INFO] [net.minecraft.server.dedicated.DedicatedServer/]: Done (1.200s)! For help, type \"help\"";
const saidJoin =
  "2026-11-06T14:06:00+0300 host java[9]: [06Nov2026 14:06:00.100] [Server thread/INFO] [net.minecraft.server.network.ServerGamePacketListenerImpl/]: <Steve> I joined the game";
const loggedIn =
  "2026-11-06T14:00:01+0300 host java[9]: [06Nov2026 14:00:01.100] [Server thread/INFO] [net.minecraft.server.players.PlayerList/]: Steve[/203.0.113.10:51234] logged in with entity id 145 at (0.5, 64.0, 0.5)";

describe("log line kinds", () => {
  it("marks minecraft errors and exceptions", () => {
    expect(isErrorLine(error)).toBe(true);
    expect(isErrorLine(fatal)).toBe(true);
    expect(isErrorLine(exception)).toBe(true);
    expect(isErrorLine(caused)).toBe(true);
    expect(isErrorLine(stack)).toBe(false);
    expect(isErrorLine(warn)).toBe(false);
    expect(isErrorLine(info)).toBe(false);
  });

  it("marks join and leave, not chat that quotes them", () => {
    expect(isSessionLine(join)).toBe(true);
    expect(isSessionLine(leave)).toBe(true);
    expect(isSessionLine(loggedIn)).toBe(true);
    expect(isSessionLine(chat)).toBe(false);
    expect(isSessionLine(saidJoin)).toBe(false);
    expect(isSessionLine(info)).toBe(false);
  });

  it("marks player chat", () => {
    expect(isChatLine(chat)).toBe(true);
    expect(isChatLine(unsigned)).toBe(true);
    expect(isChatLine(plainChat)).toBe(true);
    expect(isChatLine(saidJoin)).toBe(true);
    expect(isChatLine(join)).toBe(false);
    expect(isChatLine(info)).toBe(false);
  });

  it("filters one event and keeps the full list for all", () => {
    const lines = [info, join, loggedIn, chat, error, leave, warn, exception];
    expect(filterLogLines(lines, "all")).toEqual(lines);
    expect(filterLogLines(lines, "error")).toEqual([error, exception]);
    expect(filterLogLines(lines, "session")).toEqual([join, loggedIn, leave]);
    expect(filterLogLines(lines, "chat")).toEqual([chat]);
  });
});

describe("log file name", () => {
  it("includes the time range and the clock time", () => {
    expect(logFileName("hour", new Date(2026, 10, 6, 14, 5, 9))).toBe("log-hour-20261106-140509.txt");
    expect(logFileName("day", new Date(2026, 0, 2, 3, 4, 5))).toBe("log-day-20260102-030405.txt");
    expect(logFileName("all", new Date(2026, 10, 6, 0, 0, 0))).toBe("log-all-20261106-000000.txt");
  });
});
