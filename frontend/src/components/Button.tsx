import { forwardRef } from "react";
import clsx from "clsx";

import styles from "./Button.module.css";

type Variant = "primary" | "secondary" | "ghost" | "danger";
type Size = "sm" | "md";

type Props = React.ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: Variant;
  size?: Size;
  loading?: boolean;
};

/* Button — squared (r-6), no gradients, no shadows.
 *
 * Primary inverts the page (light fill, dark text). This is a Vercel-/Linear-
 * style move that signals "this is THE action on the page" without resorting
 * to a colored fill. Secondary is outlined, ghost is text-only, danger is
 * outlined red — keep colour scarce.
 */
export const Button = forwardRef<HTMLButtonElement, Props>(function Button(
  {
    variant = "secondary",
    size = "md",
    loading,
    children,
    className,
    disabled,
    ...rest
  },
  ref
) {
  return (
    <button
      ref={ref}
      className={clsx(styles.btn, styles[variant], styles[size], className)}
      disabled={disabled || loading}
      data-loading={loading || undefined}
      {...rest}
    >
      {loading && <span className={styles.spinner} aria-hidden />}
      <span className={styles.label}>{children}</span>
    </button>
  );
});
