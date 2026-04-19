// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0
//
// File-explorer icons — two tiers:
//
//   1. Lucide line icons (monochrome, currentColor): folder-open/closed, generic file,
//      and media/archive/lock etc.  Zero-cost — single <path>, no external dep.
//
//   2. simple-icons brand marks: language / framework badges rendered as <svg><path>.
//      We import ONLY the ~40 icons we actually use.  Vite/Rollup tree-shakes the
//      rest of the 3 000+ icon catalogue, so the actual bundle delta is ~15-20 KB
//      gzipped — not the full 900 KB people worry about.

import type { FC, SVGProps } from "react";
import {
    siBabel,
    siBun,
    siC,
    siCplusplus,
    siCss,
    siDocker,
    siDotenv,
    siEditorconfig,
    siEslint,
    siGit,
    siGnubash,
    siGo,
    siGraphql,
    siHtml5,
    siJavascript,
    siJson,
    siMarkdown,
    siNodedotjs,
    siNpm,
    siOpenjdk,
    siPhp,
    siPnpm,
    siPostcss,
    siPrettier,
    siPython,
    siReact,
    siRuby,
    siRust,
    siSass,
    siSqlite,
    siTailwindcss,
    siToml,
    siTypescript,
    siVite,
    siWebassembly,
    siWebpack,
    siXml,
    siYaml,
    siYarn,
} from "simple-icons";

type IconProps = SVGProps<SVGSVGElement> & { size?: number };

function Svg({ size = 16, children, ...rest }: IconProps & { children: React.ReactNode }) {
    return (
        <svg xmlns="http://www.w3.org/2000/svg" width={size} height={size} viewBox="0 0 24 24" {...rest}>
            {children}
        </svg>
    );
}

const LINE: React.SVGProps<SVGPathElement> = {
    fill: "none",
    stroke: "currentColor",
    strokeWidth: 1.6,
    strokeLinecap: "round" as const,
    strokeLinejoin: "round" as const,
};

// ---- Lucide line icons (monochrome, inherit currentColor) ----

export const FolderIcon: FC<IconProps> = (p) => (
    <Svg {...p}>
        <path {...LINE} d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z" />
    </Svg>
);

export const FolderOpenIcon: FC<IconProps> = (p) => (
    <Svg {...p}>
        <path {...LINE} d="m6 14 1.45-2.9A2 2 0 0 1 9.24 10H20a2 2 0 0 1 1.94 2.5l-1.55 6a2 2 0 0 1-1.94 1.5H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h3.9a2 2 0 0 1 1.69.9l.81 1.2a2 2 0 0 0 1.67.9H18a2 2 0 0 1 2 2v2" />
    </Svg>
);

export const FileIcon: FC<IconProps> = (p) => (
    <Svg {...p}>
        <path {...LINE} d="M14.5 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7.5z" />
        <path {...LINE} d="M14 2v6h6" />
    </Svg>
);

// ---- simple-icons brand marks ----
// viewBox="-3 -3 30 30" adds ~3 px padding so brand glyphs visually match the
// weight of the Lucide line icons above (which occupy ~20/24 of their canvas).

type BrandProps = IconProps & { data: { path: string; hex: string }; color?: string };
function Brand({ data, color, size = 16, ...rest }: BrandProps) {
    return (
        <svg
            xmlns="http://www.w3.org/2000/svg"
            width={size}
            height={size}
            viewBox="-3 -3 30 30"
            fill={color ?? `#${data.hex}`}
            {...rest}
        >
            <path d={data.path} />
        </svg>
    );
}

export const TsIcon: FC<IconProps> = (p) => <Brand {...p} data={siTypescript} />;
export const TsxIcon: FC<IconProps> = (p) => <Brand {...p} data={siTypescript} />;
export const JsIcon: FC<IconProps> = (p) => <Brand {...p} data={siJavascript} />;
export const JsxIcon: FC<IconProps> = (p) => <Brand {...p} data={siJavascript} />;
export const ReactTsIcon: FC<IconProps> = (p) => <Brand {...p} data={siReact} />;
export const ReactJsIcon: FC<IconProps> = (p) => <Brand {...p} data={siReact} />;
export const MdIcon: FC<IconProps> = (p) => <Brand {...p} data={siMarkdown} color="#c7c7d9" />;
export const JsonIcon: FC<IconProps> = (p) => <Brand {...p} data={siJson} />;
export const YamlIcon: FC<IconProps> = (p) => <Brand {...p} data={siYaml} />;
export const TomlIcon: FC<IconProps> = (p) => <Brand {...p} data={siToml} />;
export const XmlIcon: FC<IconProps> = (p) => <Brand {...p} data={siXml} />;
export const HtmlIcon: FC<IconProps> = (p) => <Brand {...p} data={siHtml5} />;
export const CssIcon: FC<IconProps> = (p) => <Brand {...p} data={siCss} />;
export const SassIcon: FC<IconProps> = (p) => <Brand {...p} data={siSass} />;
export const PyIcon: FC<IconProps> = (p) => <Brand {...p} data={siPython} />;
export const GoIcon: FC<IconProps> = (p) => <Brand {...p} data={siGo} />;
export const RustIcon: FC<IconProps> = (p) => <Brand {...p} data={siRust} color="#ce422b" />;
export const JavaIcon: FC<IconProps> = (p) => <Brand {...p} data={siOpenjdk} />;
export const CppIcon: FC<IconProps> = (p) => <Brand {...p} data={siCplusplus} />;
export const CIcon: FC<IconProps> = (p) => <Brand {...p} data={siC} />;
export const RubyIcon: FC<IconProps> = (p) => <Brand {...p} data={siRuby} />;
export const PhpIcon: FC<IconProps> = (p) => <Brand {...p} data={siPhp} />;
export const ShellIcon: FC<IconProps> = (p) => <Brand {...p} data={siGnubash} color="#c7c7d9" />;
export const SqlIcon: FC<IconProps> = (p) => <Brand {...p} data={siSqlite} />;
export const EnvIcon: FC<IconProps> = (p) => <Brand {...p} data={siDotenv} />;
export const DockerIcon: FC<IconProps> = (p) => <Brand {...p} data={siDocker} />;
export const GitIcon: FC<IconProps> = (p) => <Brand {...p} data={siGit} />;
export const NpmIcon: FC<IconProps> = (p) => <Brand {...p} data={siNpm} />;
export const NodeIcon: FC<IconProps> = (p) => <Brand {...p} data={siNodedotjs} />;
export const YarnIcon: FC<IconProps> = (p) => <Brand {...p} data={siYarn} />;
export const PnpmIcon: FC<IconProps> = (p) => <Brand {...p} data={siPnpm} />;
export const BunIcon: FC<IconProps> = (p) => <Brand {...p} data={siBun} color="#f9f1e1" />;
export const ViteIcon: FC<IconProps> = (p) => <Brand {...p} data={siVite} />;
export const WebpackIcon: FC<IconProps> = (p) => <Brand {...p} data={siWebpack} />;
export const TailwindIcon: FC<IconProps> = (p) => <Brand {...p} data={siTailwindcss} />;
export const PostcssIcon: FC<IconProps> = (p) => <Brand {...p} data={siPostcss} />;
export const BabelIcon: FC<IconProps> = (p) => <Brand {...p} data={siBabel} />;
export const EslintIcon: FC<IconProps> = (p) => <Brand {...p} data={siEslint} />;
export const PrettierIcon: FC<IconProps> = (p) => <Brand {...p} data={siPrettier} />;
export const EditorconfigIcon: FC<IconProps> = (p) => <Brand {...p} data={siEditorconfig} color="#c7c7d9" />;
export const WasmIcon: FC<IconProps> = (p) => <Brand {...p} data={siWebassembly} />;
export const GraphqlIcon: FC<IconProps> = (p) => <Brand {...p} data={siGraphql} />;
export const ProtobufIcon: FC<IconProps> = (p) => <FileIcon {...p} />; // no simple-icons entry

// ---- Lucide line icons with accent colour ----

const sv = (color: string) => ({ fill: "none", stroke: color, strokeWidth: 1.6, strokeLinecap: "round" as const, strokeLinejoin: "round" as const });

export const ReadmeIcon: FC<IconProps> = ({ size = 16, ...rest }) => (
    <Svg size={size} {...rest}>
        <path {...sv("#22c55e")} d="M2 4h7a3 3 0 0 1 3 3v14a3 3 0 0 0-3-3H2Z" />
        <path {...sv("#22c55e")} d="M22 4h-7a3 3 0 0 0-3 3v14a3 3 0 0 1 3-3h7Z" />
    </Svg>
);

export const LicenseIcon: FC<IconProps> = ({ size = 16, ...rest }) => (
    <Svg size={size} {...rest}>
        <path {...sv("#94a3b8")} d="M12 3v18M6 9l-3 7a3 3 0 0 0 6 0L6 9ZM18 9l-3 7a3 3 0 0 0 6 0L18 9Z" />
        <path {...sv("#94a3b8")} d="M5 21h14" />
    </Svg>
);

export const ImageIcon: FC<IconProps> = ({ size = 16, ...rest }) => (
    <Svg size={size} {...rest}>
        <rect x="3" y="3" width="18" height="18" rx="2" ry="2" fill="none" stroke="#a78bfa" strokeWidth={1.6} />
        <circle cx="9" cy="9" r="1.8" fill="#a78bfa" />
        <path {...sv("#a78bfa")} d="m21 15-3.086-3.086a2 2 0 0 0-2.828 0L6 21" />
    </Svg>
);

export const VideoIcon: FC<IconProps> = ({ size = 16, ...rest }) => (
    <Svg size={size} {...rest}>
        <rect x="3" y="4" width="18" height="16" rx="2" fill="none" stroke="#ec4899" strokeWidth={1.6} />
        <polygon points="10,9 16,12 10,15" fill="#ec4899" />
    </Svg>
);

export const AudioIcon: FC<IconProps> = ({ size = 16, ...rest }) => (
    <Svg size={size} {...rest}>
        <path {...sv("#f472b6")} d="M9 18V5l12-2v13" />
        <circle cx="6" cy="18" r="2.5" fill="#f472b6" />
        <circle cx="18" cy="16" r="2.5" fill="#f472b6" />
    </Svg>
);

export const ArchiveIcon: FC<IconProps> = ({ size = 16, ...rest }) => (
    <Svg size={size} {...rest}>
        <rect x="3" y="4" width="18" height="4" rx="1" fill="none" stroke="#a1a1aa" strokeWidth={1.6} />
        <path {...sv("#a1a1aa")} d="M5 8v10a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8" />
        <path {...sv("#a1a1aa")} d="M10 12h4" />
    </Svg>
);

export const LockIcon: FC<IconProps> = ({ size = 16, ...rest }) => (
    <Svg size={size} {...rest}>
        <rect x="4" y="10.5" width="16" height="10" rx="2" fill="none" stroke="#fbbf24" strokeWidth={1.6} />
        <path {...sv("#fbbf24")} d="M8 10.5V7a4 4 0 0 1 8 0v3.5" />
    </Svg>
);

export const DatabaseIcon: FC<IconProps> = ({ size = 16, ...rest }) => (
    <Svg size={size} {...rest}>
        <ellipse cx="12" cy="5" rx="8" ry="2.5" fill="none" stroke="#06b6d4" strokeWidth={1.6} />
        <path {...sv("#06b6d4")} d="M4 5v14c0 1.4 3.6 2.5 8 2.5s8-1.1 8-2.5V5" />
        <path {...sv("#06b6d4")} d="M4 12c0 1.4 3.6 2.5 8 2.5s8-1.1 8-2.5" />
    </Svg>
);

export const FontIcon: FC<IconProps> = ({ size = 16, ...rest }) => (
    <Svg size={size} {...rest}>
        <path {...sv("#6366f1")} d="M6 20V6h12M6 12h9" />
    </Svg>
);
