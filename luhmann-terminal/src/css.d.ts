// xterm's stylesheet is bundled as text (esbuild `--loader:.css=text`) and
// injected once on load, so a page needs one <script> and no stylesheet link
// (R3160).
declare module '*.css' {
	const content: string;
	export default content;
}
