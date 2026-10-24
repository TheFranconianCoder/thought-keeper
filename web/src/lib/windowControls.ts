/** The installed PWA runs in **window-controls-overlay** mode — the manifest asks for it — so the
 *  platform draws its own window controls INSIDE the window, over the app's own title bar, and
 *  publishes the region the app may use as `env(titlebar-area-*)`.
 *
 *  The layout reacts to that in CSS (the bar steps aside exactly where the controls are, on the right
 *  on Windows and Linux, on the left on macOS), but only while the overlay is really there — and that
 *  is all this does: one attribute on `<html>`. The `env()` variables are readable in a browser tab
 *  too, and a bar that reserved the whole window width for controls nobody draws is worse than no
 *  handling at all.
 *
 *  The geometry itself is deliberately never read here: `env()` belongs in the property that uses it.
 *  A custom property holding `env()` is invalid at computed-value time, which is what once collapsed
 *  the bar to nothing. */
export function trackWindowControls(): void {
	const wco = (
		navigator as Navigator & {
			windowControlsOverlay?: {
				visible: boolean;
				addEventListener(type: 'geometrychange', listener: () => void): void;
			};
		}
	).windowControlsOverlay;
	if (!wco) return;

	const apply = (): void => {
		document.documentElement.dataset.wco = wco.visible ? '1' : '0';
	};
	apply();
	// The overlay appears, disappears and changes size (fullscreen, a maximised window): the attribute
	// follows it, so the layout never keeps a region free that is not there any more.
	wco.addEventListener('geometrychange', apply);
}
