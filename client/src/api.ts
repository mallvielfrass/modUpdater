export {
  SERVER_URL,
  ApiError,
  setUnauthorizedHandler,
  unauthorized,
  errorCodeFromBody,
  errorCode,
  displayError,
  failureText,
  errorLog,
  displayReleaseError,
} from "./api/http";

export {
  login,
  logout,
  me,
  listAdmins,
  deleteAdmin,
  createInvite,
} from "./api/auth";

export {
  fetchPublicMods,
  fetchClientLatest,
  listAdminMods,
  listReleases,
  releaseClientVersion,
  deleteRelease,
  scanAdminMods,
  deleteMod,
  revokeMod,
  restoreMod,
  importModrinth,
  createMod,
  updateMod,
  uploadModConfig,
  deleteModConfig,
  listChangelog,
  generateChangelog,
  restartServer,
  LOG_DOWNLOAD_LINES,
  serverLogs,
  fetchRcon,
} from "./api/mods";
export type { ChangelogDay, LogWindow, ModFields, ModrinthImport, RconMod, RconStatus } from "./api/mods";

export type { BackupDest, BackupScope, S3Input, ServerBackupProgress } from "./api/backups";
export {
  asBackupScope,
  asBackupDest,
  listServerBackups,
  saveDailyBackup,
  saveBackupScope,
  saveBackupDest,
  serverBackupProgress,
  createServerBackup,
  createServerMilestone,
  saveServerS3,
  checkServerS3,
  launchServerBackup,
  deleteServerBackup,
  rollbackServerBackup,
} from "./api/backups";

export type { Datapack } from "./api/datapacks";
export {
  listDatapacks,
  uploadDatapack,
  listDatapackFiles,
  readDatapackFile,
  saveDatapackFile,
} from "./api/datapacks";
