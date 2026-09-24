import { ExternalLink } from "lucide-react";
import type { ComponentPropsWithRef, ReactNode } from "react";
import { cn } from "./cn.js";

type NativeAnchorProps = Omit<ComponentPropsWithRef<"a">, "href" | "children" | "className" | "style">;

export interface TextLinkProps extends NativeAnchorProps {
  href: string;
  children: ReactNode;
  // Inside running text: always underlined (WCAG 1.4.1).
  inline?: boolean | undefined;
  external?: boolean | undefined;
}

const BASE_CLASSES =
  "rounded-control font-normal text-primary decoration-1 underline-offset-2 focus-visible:outline-none focus-visible:shadow-focus";

// docs/components/text-link.md. Forwards `ref` and spreads anchor props so
// a router can wrap it (the shell's createLink(TextLink)).
export function TextLink({
  href,
  children,
  inline = false,
  external = false,
  className: _className,
  style: _style,
  ...rest
}: TextLinkProps & { className?: unknown; style?: unknown }): ReactNode {
  return (
    <a
      {...rest}
      href={href}
      {...(external && { target: "_blank", rel: "noopener noreferrer" })}
      className={cn(BASE_CLASSES, inline ? "underline hover:decoration-2" : "whitespace-nowrap hover:underline")}
    >
      {children}
      {external && (
        <>
          <ExternalLink size={12} aria-hidden="true" className="ms-1 inline-block align-[-0.0625em]" />
          <span className="sr-only"> (opens in a new tab)</span>
        </>
      )}
    </a>
  );
}
