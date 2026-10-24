export const ui = $state<{
  sidebarOpen: boolean;
  duplicateWindow: boolean;
}>({
  sidebarOpen: false,
  duplicateWindow: false,
});

export function toggleSidebar(): void {
  ui.sidebarOpen = !ui.sidebarOpen;
}

export function closeSidebar(): void {
  ui.sidebarOpen = false;
}
