<script lang="ts">
	import { app } from '../lib/state.svelte';
	import {
	addManyURLs,
	addPathOrURL,
	uploadFiles,
	closeAdd,
} from '../lib/actions';

	let value = $state('');
	let dragging = $state(false);
	let fileInput = $state<HTMLInputElement | undefined>(undefined);

	const isURL = (s: string): boolean => /^https?:\/\//i.test(s.trim());

	$effect(() => {
		if (!app.ui.add) return;
		const onKey = (e: KeyboardEvent): void => {
			if (e.key === 'Escape') close();
		};
		window.addEventListener('keydown', onKey);
		return () => window.removeEventListener('keydown', onKey);
	});

	function close(): void {
		closeAdd();
		value = '';
		app.sources.notice = '';
	}

	function submit(): void {
		const lines = value
			.split('\n')
			.map((s) => s.trim())
			.filter(Boolean);
		if (lines.length === 0) return;
		const allURLs = lines.every(isURL);
		if (lines.length === 1 && !allURLs) {
			void addPathOrURL(lines[0]);
		} else {
			void addManyURLs(lines);
		}
		value = '';
	}

	async function onFiles(files: FileList | null): Promise<void> {
		if (!files || files.length === 0) return;
		// A .txt/.md file whose lines are all URLs is treated as a bookmarks list.
		const file = files[0];
		if (files.length === 1 && /\.(txt|md|markdown)$/i.test(file.name)) {
			const text = await file.text();
			const lines = text
				.split('\n')
				.map((s) => s.trim())
				.filter(Boolean);
			if (lines.length > 0 && lines.every(isURL)) {
				void addManyURLs(lines);
				return;
			}
		}
		void uploadFiles(files);
	}

	function onDrop(e: DragEvent): void {
		e.preventDefault();
		dragging = false;
		void onFiles(e.dataTransfer?.files ?? null);
	}
</script>

{#if app.ui.add}
	<div class="overlay">
		<button class="overlay-bg" aria-label="Close" onclick={close}></button>
		<div class="dialog" role="dialog" aria-label="Add source" tabindex="-1">
			<h2>Add source</h2>
			<p class="sub">
				A Markdown file, a PDF, plain text, a URL — or <strong>many URLs</strong>, one per line
				(a bookmarks list). Pages that cannot be read (video, paywall, app) are kept as
				<strong>bookmarks</strong>.
			</p>

			<textarea
				class="textarea"
				bind:value={value}
				placeholder={'/path/to/file.pdf\nhttps://example.com/article\nhttps://another.example.org/post'}
				onkeydown={(e) => {
					if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) submit();
				}}
			></textarea>

			<div class="dialog-foot">
				<button class="btn btn-accent" onclick={submit}>Add</button>
				<span class="muted" style="font-size:12px">⌘/Ctrl+⏎</span>
			</div>

			<div
				class="dropzone {dragging ? 'drop-active' : ''}"
				role="button"
				tabindex="0"
				onclick={() => fileInput?.click()}
				onkeydown={(e) => e.key === 'Enter' && fileInput?.click()}
				ondragover={(e) => {
					e.preventDefault();
					dragging = true;
				}}
				ondragleave={() => (dragging = false)}
				ondrop={onDrop}
			>
				Drop files here (a <code>.txt</code> of URLs becomes a batch), or <u>browse…</u>
			</div>
			<input type="file" multiple hidden bind:this={fileInput} onchange={() => onFiles(fileInput?.files ?? null)} />

			<div class="foot">
				<span class="msg">{app.sources.notice}</span>
				<button class="btn btn-ghost" onclick={close}>Close</button>
			</div>
		</div>
	</div>
{/if}
